package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

var shopRateState = struct {
	sync.Mutex
	items map[string]shopActionRateWindow
}{items: make(map[string]shopActionRateWindow)}

type shopActionRateWindow struct {
	start   time.Time
	expires time.Time
	count   int
}

// shopRateWindow is also used by the independent order-lookup limiter.
type shopRateWindow struct {
	start time.Time
	count int
}

func shopRateLimit(c echo.Context, action string, limit int, window time.Duration) bool {
	now := time.Now()
	remote := c.RealIP()
	if remote == "" {
		remote = c.Request().RemoteAddr
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
	}
	key := action + ":" + remote
	shopRateState.Lock()
	defer shopRateState.Unlock()
	for k, entry := range shopRateState.items {
		if !entry.expires.After(now) {
			delete(shopRateState.items, k)
		}
	}
	entry := shopRateState.items[key]
	if entry.start.IsZero() || !entry.expires.After(now) {
		shopRateState.items[key] = shopActionRateWindow{start: now, expires: now.Add(window), count: 1}
		return false
	}
	entry.count++
	shopRateState.items[key] = entry
	return entry.count > limit
}

func shopDecodeJSON(c echo.Context, target any) error {
	if c.Request().Body == nil {
		return errors.New("missing JSON body")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(c.Request().Header.Get(echo.HeaderContentType), ";")[0]))
	if contentType != echo.MIMEApplicationJSON {
		return errors.New("Content-Type must be application/json")
	}
	c.Request().Body = httpMaxBytes(c.Request().Body, 1<<20)
	decoder := json.NewDecoder(c.Request().Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func httpMaxBytes(body io.ReadCloser, limit int64) io.ReadCloser {
	return &limitedReadCloser{Reader: io.LimitReader(body, limit+1), closer: body}
}

type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *limitedReadCloser) Close() error { return r.closer.Close() }

func shopValidOrigin(c echo.Context) bool {
	origin := strings.TrimSpace(c.Request().Header.Get(echo.HeaderOrigin))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := c.Request().Host
	return strings.EqualFold(u.Host, host)
}

var (
	shopEmailPattern    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	shopMobilePattern   = regexp.MustCompile(`^09\d{8}$`)
	shopCarrierPattern  = regexp.MustCompile(`^/[A-Z0-9.+\-]{7}$`)
	shopDonationPattern = regexp.MustCompile(`^\d{3,7}$`)
)

func shopValidEmail(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) > 255 || !shopEmailPattern.MatchString(value) {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(parsed.Address, value)
}

func shopValidPhone(value string) bool {
	return shopMobilePattern.MatchString(strings.TrimSpace(value))
}

func shopValidInvoice(input shopInvoiceInput) string {
	switch input.Kind {
	case "none":
		return ""
	case "personal_carrier":
		if !shopCarrierPattern.MatchString(strings.TrimSpace(input.CarrierCode)) {
			return "carrier_code"
		}
	case "tax_id":
		if !shopValidTaxID(strings.TrimSpace(input.TaxID)) {
			return "tax_id"
		}
		if strings.TrimSpace(input.CompanyName) == "" || len(input.CompanyName) > 120 {
			return "company_name"
		}
	case "donation":
		if !shopDonationPattern.MatchString(strings.TrimSpace(input.DonationCode)) {
			return "donation_code"
		}
	default:
		return "kind"
	}
	return ""
}

func shopValidTaxID(value string) bool {
	if len(value) != 8 {
		return false
	}
	weights := [...]int{1, 2, 1, 2, 1, 2, 4, 1}
	sum := 0
	for i, r := range value {
		if r < '0' || r > '9' {
			return false
		}
		n := int(r-'0') * weights[i]
		sum += n/10 + n%10
	}
	return sum%5 == 0 || (value[6] == '7' && (sum+1)%5 == 0)
}

func shopJSONHasOnlyOneValue(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var v any
	if decoder.Decode(&v) != nil {
		return false
	}
	return decoder.Decode(&v) == io.EOF
}
