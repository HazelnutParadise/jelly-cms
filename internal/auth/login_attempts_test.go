package auth_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/labstack/echo/v4"
)

const loginAttemptBody = `{"username":` // Deliberately malformed: the real login handler must return 400 without touching PostgreSQL.

func loginAttemptRequest(e *echo.Echo, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(loginAttemptBody))
	req.RemoteAddr = remoteAddr
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Jelly-CSRF", "1")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func loginAttemptRouter() *echo.Echo {
	e := echo.New()
	auth.RegisterRoutes(e)
	return e
}

func assertLoginThrottleResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if !strings.Contains(strings.ToLower(rec.Header().Get(echo.HeaderContentType)), "application/json") {
		t.Errorf("429 content type = %q, want JSON", rec.Header().Get(echo.HeaderContentType))
	}
	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || strings.TrimSpace(payload["error"]) == "" {
		t.Errorf("429 body must contain a non-empty JSON error (decode error: %v)", err)
	}
	if rec.Header().Values("Set-Cookie") != nil {
		for _, cookie := range rec.Header().Values("Set-Cookie") {
			if strings.HasPrefix(cookie, "jwt_token=") || strings.HasPrefix(cookie, "jelly-cms-session=") {
				t.Errorf("429 response set login cookie %q", strings.SplitN(cookie, "=", 2)[0])
			}
		}
	}
}

func TestLoginAttemptWindowExpiryAndRetryAfterDoNotExtend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := loginAttemptRouter()
		for attempt := 1; attempt <= 30; attempt++ {
			got := loginAttemptRequest(e, "192.0.2.1:41000", nil)
			if got.Code != http.StatusBadRequest {
				t.Fatalf("attempt %d returned %d, want downstream 400", attempt, got.Code)
			}
		}
		start := time.Now()
		firstReject := loginAttemptRequest(e, "192.0.2.1:41000", nil)
		if firstReject.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt 31 returned %d, want 429", firstReject.Code)
		}
		assertLoginThrottleResponse(t, firstReject)
		firstRetry, err := strconv.Atoi(firstReject.Header().Get("Retry-After"))
		if err != nil || firstRetry != 60 {
			t.Fatalf("Retry-After = %q at window start, want 60", firstReject.Header().Get("Retry-After"))
		}

		time.Sleep(10*time.Second + 500*time.Millisecond)
		secondReject := loginAttemptRequest(e, "192.0.2.1:41000", nil)
		if secondReject.Code != http.StatusTooManyRequests {
			t.Fatalf("retry at +10s returned %d, want 429", secondReject.Code)
		}
		assertLoginThrottleResponse(t, secondReject)
		secondRetry, err := strconv.Atoi(secondReject.Header().Get("Retry-After"))
		if err != nil || secondRetry != 50 {
			t.Fatalf("Retry-After = %q at +10.5s, want ceil(49.5s)=50", secondReject.Header().Get("Retry-After"))
		}

		time.Sleep(49 * time.Second)
		thirdReject := loginAttemptRequest(e, "192.0.2.1:41000", nil)
		if thirdReject.Code != http.StatusTooManyRequests {
			t.Fatalf("retry at +59.5s returned %d, want 429", thirdReject.Code)
		}
		assertLoginThrottleResponse(t, thirdReject)
		thirdRetry, err := strconv.Atoi(thirdReject.Header().Get("Retry-After"))
		if err != nil || thirdRetry != 1 {
			t.Fatalf("Retry-After = %q at +59.5s, want ceil(0.5s)=1", thirdReject.Header().Get("Retry-After"))
		}
		if elapsed := time.Since(start); elapsed != 59*time.Second+500*time.Millisecond {
			t.Fatalf("virtual time advanced %s, want exactly 59.5s", elapsed)
		}

		time.Sleep(500 * time.Millisecond)
		afterExpiry := loginAttemptRequest(e, "192.0.2.1:41000", nil)
		if afterExpiry.Code != http.StatusBadRequest {
			t.Fatalf("request after original 60s window returned %d, want downstream 400", afterExpiry.Code)
		}
	})
}

func TestLoginAttemptParallelLimitIsExactlyThirty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := loginAttemptRouter()
		var wg sync.WaitGroup
		statuses := make(chan int, 48)
		for i := 0; i < 48; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses <- loginAttemptRequest(e, "192.0.2.2:41000", nil).Code
			}()
		}
		wg.Wait()
		close(statuses)
		counts := map[int]int{}
		for status := range statuses {
			counts[status]++
		}
		if counts[http.StatusBadRequest] != 30 || counts[http.StatusTooManyRequests] != 18 || len(counts) != 2 {
			t.Fatalf("parallel responses = %v, want exactly 30 downstream 400 and 18 throttled 429", counts)
		}
	})
}

func TestLoginAttemptSourceIdentityAndForwardedHeaderIsolation(t *testing.T) {
	tests := []struct {
		name       string
		firstAddr  string
		secondAddr string
		headers    map[string]string
	}{
		{name: "port change", firstAddr: "192.0.2.3:41000", secondAddr: "192.0.2.3:51000"},
		{name: "equivalent IPv6", firstAddr: "[2001:0db8:0:0:0:0:0:1]:41000", secondAddr: "[2001:db8::1]:51000"},
		{name: "mapped IPv4", firstAddr: "192.0.2.4:41000", secondAddr: "[::ffff:192.0.2.4]:51000"},
		{name: "X-Forwarded-For", firstAddr: "192.0.2.5:41000", secondAddr: "192.0.2.5:51000", headers: map[string]string{"X-Forwarded-For": "198.51.100.2"}},
		{name: "X-Real-IP", firstAddr: "192.0.2.6:41000", secondAddr: "192.0.2.6:51000", headers: map[string]string{"X-Real-IP": "198.51.100.2"}},
		{name: "Forwarded", firstAddr: "192.0.2.7:41000", secondAddr: "192.0.2.7:51000", headers: map[string]string{"Forwarded": "for=198.51.100.2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := loginAttemptRouter()
				for i := 0; i < 30; i++ {
					if got := loginAttemptRequest(e, tc.firstAddr, nil).Code; got != http.StatusBadRequest {
						t.Fatalf("initial attempt %d returned %d, want 400", i+1, got)
					}
				}
				if got := loginAttemptRequest(e, tc.secondAddr, tc.headers).Code; got != http.StatusTooManyRequests {
					t.Fatalf("same source via %s returned %d, want 429", tc.name, got)
				}
			})
		})
	}
}

func TestLoginAttemptInvalidRemoteAddressesShareOneBucket(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := loginAttemptRouter()
		for i := 0; i < 30; i++ {
			if got := loginAttemptRequest(e, "not-an-ip", nil).Code; got != http.StatusBadRequest {
				t.Fatalf("invalid source attempt %d returned %d, want 400", i+1, got)
			}
		}
		for _, invalid := range []string{"", "forged.invalid:80", "192.0.2.1"} {
			if got := loginAttemptRequest(e, invalid, nil).Code; got != http.StatusTooManyRequests {
				t.Errorf("invalid RemoteAddr %q returned %d, want shared-bucket 429", invalid, got)
			}
		}
	})
}

func TestLoginAttemptRouterStateIsIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := loginAttemptRouter(), loginAttemptRouter()
		for i := 0; i < 30; i++ {
			if got := loginAttemptRequest(first, "192.0.2.8:41000", nil).Code; got != http.StatusBadRequest {
				t.Fatalf("first router attempt %d returned %d, want 400", i+1, got)
			}
		}
		if got := loginAttemptRequest(first, "192.0.2.8:41000", nil).Code; got != http.StatusTooManyRequests {
			t.Fatalf("first router attempt 31 returned %d, want 429", got)
		}
		if got := loginAttemptRequest(second, "192.0.2.8:41000", nil).Code; got != http.StatusBadRequest {
			t.Fatalf("new router inherited another router's state: status %d, want 400", got)
		}
	})
}

func TestLoginAttemptCapacityDoesNotEvictLiveSourcesAndRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := loginAttemptRouter()
		for i := 0; i < 4096; i++ {
			addr := fmt.Sprintf("198.18.%d.%d:%d", i/256, i%256, 40000+i%1000)
			if got := loginAttemptRequest(e, addr, nil).Code; got != http.StatusBadRequest {
				t.Fatalf("new source %d returned %d, want downstream 400", i, got)
			}
		}
		if got := loginAttemptRequest(e, "203.0.113.1:41000", nil).Code; got != http.StatusTooManyRequests {
			t.Fatalf("source beyond 4096-entry capacity returned %d, want 429", got)
		}
		for i := 1; i < 30; i++ {
			if got := loginAttemptRequest(e, "198.18.0.0:40000", nil).Code; got != http.StatusBadRequest {
				t.Fatalf("existing live source attempt %d returned %d, want downstream 400", i+1, got)
			}
		}
		if got := loginAttemptRequest(e, "198.18.0.0:40000", nil).Code; got != http.StatusTooManyRequests {
			t.Fatalf("existing source attempt 31 returned %d, want 429", got)
		}
		time.Sleep(60 * time.Second)
		if got := loginAttemptRequest(e, "203.0.113.1:41000", nil).Code; got != http.StatusBadRequest {
			t.Fatalf("new source after expiry returned %d, want downstream 400", got)
		}
	})
}
