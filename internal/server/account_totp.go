package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	qrcode "github.com/skip2/go-qrcode"
)

func accountTOTPStatus(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	enabled, err := auth.TOTPEnabled(user.ID)
	if err != nil {
		log.Printf("load account two-step verification status for user %d: %v", user.ID, err)
		return accountServerError(c, "Unable to load two-step verification status")
	}
	var record core.UserTOTP
	if enabled {
		err = data.DB.WithContext(c.Request().Context()).Where("user_id = ?", user.ID).First(&record).Error
		if err != nil {
			log.Printf("load account two-step verification record for user %d: %v", user.ID, err)
			return accountServerError(c, "Unable to load two-step verification status")
		}
	}
	var remaining int64
	if enabled {
		if err := data.DB.WithContext(c.Request().Context()).Model(&core.RecoveryCode{}).
			Where("user_id = ? AND used_at IS NULL", user.ID).Count(&remaining).Error; err != nil {
			log.Printf("count account recovery codes for user %d: %v", user.ID, err)
			return accountServerError(c, "Unable to load recovery-code status")
		}
	}
	var enabledAt *time.Time
	if enabled {
		enabledAt = record.EnabledAt
	}
	return c.JSON(http.StatusOK, map[string]any{
		"enabled": enabled, "enabled_at": enabledAt, "recovery_remaining": remaining,
	})
}

func enrollAccountTOTP(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	secret, uri, err := auth.EnrollTOTP(user.ID)
	if err != nil {
		return mapTOTPError(c, err)
	}
	qr, err := totpQRCodeSVG(uri)
	if err != nil {
		log.Printf("generate account authenticator QR code for user %d: %v", user.ID, err)
		return accountServerError(c, "Unable to generate the authenticator QR code")
	}
	return c.JSON(http.StatusOK, map[string]string{"secret": secret, "otpauth_uri": uri, "qr_svg": qr})
}

func verifyAccountTOTP(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := c.Bind(&req); err != nil || req.Code == "" {
		return accountBadRequest(c, "A verification code is required")
	}
	codes, err := auth.VerifyTOTPEnrollment(user.ID, req.Code)
	if err != nil {
		return mapTOTPError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"recovery_codes": codes})
}

func disableAccountTOTP(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
		RecoveryCode    string `json:"recovery_code"`
	}
	if err := c.Bind(&req); err != nil {
		return accountBadRequest(c, "Invalid request")
	}
	if req.Code == "" && req.RecoveryCode == "" {
		return accountBadRequest(c, "A verification or recovery code is required")
	}
	if err := auth.DisableTOTP(user.ID, req.CurrentPassword, req.Code, req.RecoveryCode); err != nil {
		return mapTOTPError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Two-step verification disabled"})
}

func replaceAccountTOTPRecoveryCodes(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := c.Bind(&req); err != nil || req.Code == "" {
		return accountBadRequest(c, "A verification code is required")
	}
	codes, err := auth.ReplaceTOTPRecoveryCodes(user.ID, req.Code)
	if err != nil {
		return mapTOTPError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"recovery_codes": codes})
}

func completeAccountMFALogin(c echo.Context) error {
	var req struct {
		MFAToken     string `json:"mfa_token"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := c.Bind(&req); err != nil || req.MFAToken == "" || (req.Code == "" && req.RecoveryCode == "") {
		return accountBadRequest(c, "mfa_token and a verification or recovery code are required")
	}
	token, user, err := auth.CompleteMFALogin(c, req.MFAToken, req.Code, req.RecoveryCode)
	if err != nil {
		return mapTOTPError(c, err)
	}
	c.SetCookie(&http.Cookie{
		Name: "jwt_token", Value: token, Path: "/", MaxAge: 86400 * 7,
		HttpOnly: true, Secure: os.Getenv("APP_ENV") == "production", SameSite: http.SameSiteLaxMode,
	})
	return c.JSON(http.StatusOK, map[string]any{"message": "Login successful", "user": user, "token": token})
}

func mapTOTPError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, auth.ErrMFALocked):
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "Two-step verification is temporarily locked"})
	case errors.Is(err, auth.ErrMFAInvalid), errors.Is(err, auth.ErrPasswordInvalid):
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid or expired verification"})
	case errors.Is(err, auth.ErrTOTPAlreadyEnabled), errors.Is(err, auth.ErrTOTPNotEnabled):
		return c.JSON(http.StatusConflict, map[string]string{"error": "Two-step verification state conflict"})
	default:
		log.Printf("account two-step verification failed: %v", err)
		return accountServerError(c, "Unable to complete two-step verification")
	}
}

func totpQRCodeSVG(payload string) (string, error) {
	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("encode authenticator URI: %w", err)
	}
	bitmap := code.Bitmap()
	size := len(bitmap)
	if size == 0 {
		return "", fmt.Errorf("QR encoder returned an empty bitmap")
	}

	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" role="img" aria-label="Authenticator QR code"><rect width="100%%" height="100%%" fill="#fff"/><path d="`, size, size)
	for y, row := range bitmap {
		if len(row) != size {
			return "", fmt.Errorf("QR encoder returned a non-square bitmap")
		}
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&svg, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	svg.WriteString(`" fill="#000"/></svg>`)
	return svg.String(), nil
}
