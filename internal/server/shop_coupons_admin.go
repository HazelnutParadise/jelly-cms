package server

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const couponListMaxPageSize = 100

var couponCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type couponAdminError struct {
	Message string            `json:"error"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type couponAdminResponse struct {
	core.Coupon
	Targets []map[string]any `json:"targets,omitempty"`
}

func couponBadRequest(c echo.Context, message string) error {
	return c.JSON(http.StatusBadRequest, couponAdminError{Message: message})
}

func couponInvalid(c echo.Context, fields map[string]string) error {
	return c.JSON(http.StatusUnprocessableEntity, couponAdminError{Message: "invalid coupon", Fields: fields})
}

func couponInternalError(c echo.Context, err error) error {
	// Do not leak SQL or driver details to administrators' browsers.
	c.Logger().Errorf("shop coupon admin operation failed: %v", err)
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

func couponID(c echo.Context) (uint, error) {
	value, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || value == 0 || value > uint64(^uint(0)) {
		return 0, errors.New("invalid coupon id")
	}
	return uint(value), nil
}

func decodeCouponObject(c echo.Context, allowed map[string]bool) (map[string]json.RawMessage, error) {
	if c.Request().Body == nil {
		return nil, errors.New("missing JSON body")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(c.Request().Header.Get(echo.HeaderContentType), ";")[0]))
	if contentType != echo.MIMEApplicationJSON {
		return nil, errors.New("Content-Type must be application/json")
	}
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 1<<20 {
		return nil, errors.New("JSON body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("JSON body must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	for key := range fields {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown field %q", key)
		}
	}
	return fields, nil
}

func couponField[T any](fields map[string]json.RawMessage, key string, current T) (T, bool, error) {
	raw, present := fields[key]
	if !present {
		return current, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return current, true, fmt.Errorf("%s cannot be null", key)
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return current, true, fmt.Errorf("%s has the wrong type", key)
	}
	return value, true, nil
}

func couponNullableInt(fields map[string]json.RawMessage, key string, current *int) (*int, bool, error) {
	raw, present := fields[key]
	if !present {
		return current, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return current, true, fmt.Errorf("%s must be an integer or null", key)
	}
	return &value, true, nil
}

func couponNullableTime(fields map[string]json.RawMessage, key string, current *time.Time) (*time.Time, bool, error) {
	raw, present := fields[key]
	if !present {
		return current, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return current, true, fmt.Errorf("%s must be an RFC3339 timestamp or null", key)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return current, true, fmt.Errorf("%s must be an RFC3339 timestamp or null", key)
	}
	return &parsed, true, nil
}

func couponTargetsInput(fields map[string]json.RawMessage, current []core.CouponTarget) ([]core.CouponTarget, bool, error) {
	raw, present := fields["targets"]
	if !present {
		return current, false, nil
	}
	var inputs []struct {
		Type       string `json:"type"`
		TargetType string `json:"target_type"`
		TargetID   uint   `json:"target_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inputs); err != nil || inputs == nil {
		return nil, true, errors.New("targets must be an array")
	}
	result := make([]core.CouponTarget, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		targetType := input.TargetType
		if input.TargetType != "" && input.Type != "" && input.TargetType != input.Type {
			return nil, true, errors.New("target_type and type disagree")
		}
		if targetType == "" {
			targetType = input.Type
		}
		if input.TargetID == 0 || (targetType != "category" && targetType != "product") {
			return nil, true, errors.New("each target needs a positive id and category or product type")
		}
		key := targetType + ":" + strconv.FormatUint(uint64(input.TargetID), 10)
		if _, ok := seen[key]; ok {
			return nil, true, errors.New("duplicate coupon target")
		}
		seen[key] = struct{}{}
		result = append(result, core.CouponTarget{TargetType: targetType, TargetID: input.TargetID})
	}
	return result, true, nil
}

func couponCandidate(fields map[string]json.RawMessage, base core.Coupon, create bool) (core.Coupon, []core.CouponTarget, bool, map[string]string) {
	coupon := base
	fieldErrors := map[string]string{}
	stringField := func(key string, dst *string, required bool) {
		value, present, err := couponField(fields, key, *dst)
		if err != nil {
			fieldErrors[key] = err.Error()
			return
		}
		if required && (!present || strings.TrimSpace(value) == "") {
			fieldErrors[key] = key + " is required"
			return
		}
		if present {
			*dst = value
		}
	}
	int64Field := func(key string, dst *int64, required bool) {
		value, present, err := couponField(fields, key, *dst)
		if err != nil {
			fieldErrors[key] = err.Error()
			return
		}
		if required && !present {
			fieldErrors[key] = key + " is required"
			return
		}
		if present {
			*dst = value
		}
	}
	intField := func(key string, dst *int, required bool) {
		value, present, err := couponField(fields, key, *dst)
		if err != nil {
			fieldErrors[key] = err.Error()
			return
		}
		if required && !present {
			fieldErrors[key] = key + " is required"
			return
		}
		if present {
			*dst = value
		}
	}
	boolField := func(key string, dst *bool) {
		value, present, err := couponField(fields, key, *dst)
		if err != nil {
			fieldErrors[key] = err.Error()
		} else if present {
			*dst = value
		}
	}
	stringField("code", &coupon.Code, create)
	stringField("label", &coupon.Label, false)
	stringField("kind", &coupon.Kind, create)
	int64Field("value", &coupon.Value, create)
	intField("buy_qty", &coupon.BuyQty, false)
	intField("get_qty", &coupon.GetQty, false)
	int64Field("min_subtotal", &coupon.MinSubtotal, false)
	stringField("scope", &coupon.Scope, false)
	var err error
	coupon.UsageLimit, _, err = couponNullableInt(fields, "usage_limit", coupon.UsageLimit)
	if err != nil {
		fieldErrors["usage_limit"] = err.Error()
	}
	coupon.PerUserLimit, _, err = couponNullableInt(fields, "per_user_limit", coupon.PerUserLimit)
	if err != nil {
		fieldErrors["per_user_limit"] = err.Error()
	}
	coupon.StartsAt, _, err = couponNullableTime(fields, "starts_at", coupon.StartsAt)
	if err != nil {
		fieldErrors["starts_at"] = err.Error()
	}
	coupon.EndsAt, _, err = couponNullableTime(fields, "ends_at", coupon.EndsAt)
	if err != nil {
		fieldErrors["ends_at"] = err.Error()
	}
	boolField("stackable_with_free_shipping", &coupon.StackableWithFreeShipping)
	boolField("members_only", &coupon.MembersOnly)
	stringField("bound_email", &coupon.BoundEmail, false)
	stringField("status", &coupon.Status, false)

	if create {
		if _, present := fields["label"]; !present {
			coupon.Label = ""
		}
		if _, present := fields["buy_qty"]; !present {
			coupon.BuyQty = 0
		}
		if _, present := fields["get_qty"]; !present {
			coupon.GetQty = 0
		}
		if _, present := fields["min_subtotal"]; !present {
			coupon.MinSubtotal = 0
		}
		if _, present := fields["scope"]; !present {
			coupon.Scope = "all"
		}
		if _, present := fields["status"]; !present {
			coupon.Status = "active"
		}
	}
	coupon.Code = strings.TrimSpace(coupon.Code)
	coupon.Label = strings.TrimSpace(coupon.Label)
	coupon.Scope = strings.TrimSpace(coupon.Scope)
	coupon.Status = strings.TrimSpace(coupon.Status)
	coupon.BoundEmail = strings.ToLower(strings.TrimSpace(coupon.BoundEmail))

	if _, present := fields["code"]; present && coupon.Code == "" {
		fieldErrors["code"] = "code is required"
	}
	if coupon.Code != "" && !couponCodePattern.MatchString(coupon.Code) {
		fieldErrors["code"] = "use 1 to 64 letters, numbers, underscores, or hyphens"
	}
	if utf8.RuneCountInString(coupon.Label) > 120 {
		fieldErrors["label"] = "label must be at most 120 characters"
	}
	switch coupon.Kind {
	case "percent":
		if coupon.Value < 0 || coupon.Value > 100 {
			fieldErrors["value"] = "percent must be between 0 and 100"
		}
		if coupon.BuyQty != 0 || coupon.GetQty != 0 {
			fieldErrors["buy_qty"] = "quantity fields are only valid for bogo coupons"
		}
	case "fixed", "free_shipping":
		if coupon.Value < 0 {
			fieldErrors["value"] = "value cannot be negative"
		}
		if coupon.Kind == "free_shipping" && coupon.Value != 0 {
			fieldErrors["value"] = "free shipping value must be zero"
		}
		if coupon.BuyQty != 0 || coupon.GetQty != 0 {
			fieldErrors["buy_qty"] = "quantity fields are only valid for bogo coupons"
		}
	case "bogo":
		if coupon.Value < 0 {
			fieldErrors["value"] = "value cannot be negative"
		}
		if coupon.BuyQty < 1 || coupon.GetQty < 1 || coupon.BuyQty > math.MaxInt-coupon.GetQty {
			fieldErrors["buy_qty"] = "buy and get quantities must be positive and within range"
		}
	default:
		fieldErrors["kind"] = "unsupported coupon kind"
	}
	if coupon.MinSubtotal < 0 {
		fieldErrors["min_subtotal"] = "minimum subtotal cannot be negative"
	}
	if coupon.UsageLimit != nil && *coupon.UsageLimit < 0 {
		fieldErrors["usage_limit"] = "usage limit cannot be negative"
	}
	if coupon.PerUserLimit != nil && *coupon.PerUserLimit < 0 {
		fieldErrors["per_user_limit"] = "per-user limit cannot be negative"
	}
	if coupon.Scope != "all" && coupon.Scope != "category" && coupon.Scope != "product" {
		fieldErrors["scope"] = "unsupported coupon scope"
	}
	if coupon.Status != "active" && coupon.Status != "disabled" {
		fieldErrors["status"] = "status must be active or disabled"
	}
	if coupon.BoundEmail != "" && !shopValidEmail(coupon.BoundEmail) {
		fieldErrors["bound_email"] = "enter a valid email address"
	}
	if coupon.StartsAt != nil && coupon.EndsAt != nil && !coupon.EndsAt.After(*coupon.StartsAt) {
		fieldErrors["ends_at"] = "end must be later than start"
	}

	targets, targetProvided, targetErr := couponTargetsInput(fields, nil)
	if targetErr != nil {
		fieldErrors["targets"] = targetErr.Error()
	}
	if targetProvided {
		for _, target := range targets {
			if coupon.Scope == "all" || target.TargetType != coupon.Scope {
				fieldErrors["targets"] = "target types must match the coupon scope"
				break
			}
		}
	}
	if coupon.Scope == "all" && targetProvided && len(targets) != 0 {
		fieldErrors["targets"] = "all-scope coupons cannot have targets"
	}
	if coupon.Scope != "all" && (targetProvided && len(targets) == 0 || create && !targetProvided) {
		fieldErrors["targets"] = "scoped coupons need at least one target"
	}
	return coupon, targets, targetProvided, fieldErrors
}

func couponInputKeys() map[string]bool {
	return map[string]bool{"code": true, "label": true, "kind": true, "value": true, "buy_qty": true, "get_qty": true,
		"min_subtotal": true, "scope": true, "usage_limit": true, "per_user_limit": true, "starts_at": true,
		"ends_at": true, "stackable_with_free_shipping": true, "members_only": true, "bound_email": true,
		"status": true, "targets": true}
}

func couponConflict(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var state interface{ SQLState() string }
	return errors.As(err, &state) && state.SQLState() == "23505"
}

func couponHasHistory(tx *gorm.DB, coupon core.Coupon) (bool, error) {
	if coupon.UsedCount > 0 {
		return true, nil
	}
	var count int64
	if err := tx.Model(&core.CouponRedemption{}).Where("coupon_id = ?", coupon.ID).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	if err := tx.Model(&core.Order{}).Where("coupon_id = ?", coupon.ID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func validateCouponTargets(tx *gorm.DB, coupon core.Coupon, targets []core.CouponTarget) error {
	if coupon.Scope == "all" {
		if len(targets) != 0 {
			return &couponValidationError{fields: map[string]string{"targets": "all-scope coupons cannot have targets"}}
		}
		return nil
	}
	if len(targets) == 0 {
		return &couponValidationError{fields: map[string]string{"targets": "scoped coupons need at least one target"}}
	}
	categoryIDs, productIDs := make([]uint, 0, len(targets)), make([]uint, 0, len(targets))
	for _, target := range targets {
		if target.TargetType != coupon.Scope {
			return &couponValidationError{fields: map[string]string{"targets": "target type must match coupon scope"}}
		}
		if target.TargetType == "category" {
			categoryIDs = append(categoryIDs, target.TargetID)
		} else if target.TargetType == "product" {
			productIDs = append(productIDs, target.TargetID)
		} else {
			return &couponValidationError{fields: map[string]string{"targets": "unsupported target type"}}
		}
	}
	if len(categoryIDs) > 0 {
		var count int64
		if err := tx.Model(&core.ProductCategory{}).Where("id IN ?", categoryIDs).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(categoryIDs)) {
			return &couponValidationError{fields: map[string]string{"targets": "one or more categories do not exist"}}
		}
	}
	if len(productIDs) > 0 {
		var count int64
		if err := tx.Model(&core.Product{}).Where("id IN ? AND deleted_at IS NULL", productIDs).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(productIDs)) {
			return &couponValidationError{fields: map[string]string{"targets": "one or more products do not exist"}}
		}
	}
	return nil
}

func replaceCouponTargets(tx *gorm.DB, couponID uint, targets []core.CouponTarget) error {
	if err := tx.Where("coupon_id = ?", couponID).Delete(&core.CouponTarget{}).Error; err != nil {
		return err
	}
	for i := range targets {
		targets[i].CouponID = couponID
	}
	if len(targets) == 0 {
		return nil
	}
	return tx.Create(&targets).Error
}

func couponTargetViews(tx *gorm.DB, couponID uint) ([]map[string]any, error) {
	var targets []core.CouponTarget
	if err := tx.Where("coupon_id = ?", couponID).Order("id").Find(&targets).Error; err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		name := ""
		switch target.TargetType {
		case "category":
			var category core.ProductCategory
			if err := tx.Select("id", "name").First(&category, target.TargetID).Error; err != nil {
				return nil, err
			}
			name = category.Name
		case "product":
			var product core.Product
			if err := tx.Unscoped().Select("id", "title").First(&product, target.TargetID).Error; err != nil {
				return nil, err
			}
			name = product.Title
		}
		views = append(views, map[string]any{"target_type": target.TargetType, "target_id": target.TargetID, "name": name})
	}
	return views, nil
}

func adminCouponsList(c echo.Context) error {
	status := strings.TrimSpace(c.QueryParam("status"))
	if status != "" && status != "active" && status != "scheduled" && status != "ended" {
		return couponBadRequest(c, "invalid status filter")
	}
	q := strings.TrimSpace(c.QueryParam("q"))
	if utf8.RuneCountInString(q) > 120 {
		return couponBadRequest(c, "search query is too long")
	}
	page, perPage := 1, couponListMaxPageSize
	if value := c.QueryParam("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			return couponBadRequest(c, "invalid page")
		}
		page = parsed
	}
	if value := c.QueryParam("per_page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > couponListMaxPageSize {
			return couponBadRequest(c, "invalid per_page")
		}
		perPage = parsed
	}
	if page > math.MaxInt/perPage {
		return couponBadRequest(c, "page is too large")
	}
	now := time.Now()
	applyStatus := func(db *gorm.DB, filter string) *gorm.DB {
		switch filter {
		case "active":
			return db.Where("status = ? AND (starts_at IS NULL OR starts_at <= ?) AND (ends_at IS NULL OR ends_at > ?)", "active", now, now)
		case "scheduled":
			return db.Where("status = ? AND starts_at IS NOT NULL AND starts_at > ?", "active", now)
		case "ended":
			return db.Where("status != ? OR (ends_at IS NOT NULL AND ends_at <= ?)", "active", now)
		default:
			return db
		}
	}
	query := applyStatus(data.DB.Model(&core.Coupon{}), status)
	if q != "" {
		pattern := "%" + strings.ToLower(q) + "%"
		query = query.Where("lower(code) LIKE ? OR lower(label) LIKE ?", pattern, pattern)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return couponInternalError(c, err)
	}
	var coupons []core.Coupon
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&coupons).Error; err != nil {
		return couponInternalError(c, err)
	}
	counts := map[string]int64{}
	for _, segment := range []string{"active", "scheduled", "ended"} {
		var count int64
		if err := applyStatus(data.DB.Model(&core.Coupon{}), segment).Count(&count).Error; err != nil {
			return couponInternalError(c, err)
		}
		counts[segment] = count
	}
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var orders, discountTotal, revenue int64
	base := data.DB.Model(&core.Order{}).Where("coupon_id IS NOT NULL AND created_at >= ?", startOfMonth)
	if err := base.Count(&orders).Error; err != nil {
		return couponInternalError(c, err)
	}
	if err := base.Select("COALESCE(SUM(discount_total),0)").Scan(&discountTotal).Error; err != nil {
		return couponInternalError(c, err)
	}
	if err := base.Select("COALESCE(SUM(total_amount),0)").Scan(&revenue).Error; err != nil {
		return couponInternalError(c, err)
	}
	ids := make([]uint, 0, len(coupons))
	for _, coupon := range coupons {
		ids = append(ids, coupon.ID)
	}
	var targetRows []core.CouponTarget
	if len(ids) > 0 {
		if err := data.DB.Where("coupon_id IN ?", ids).Order("id").Find(&targetRows).Error; err != nil {
			return couponInternalError(c, err)
		}
	}
	targetsByCoupon := make(map[uint][]map[string]any, len(ids))
	for _, target := range targetRows {
		targetsByCoupon[target.CouponID] = append(targetsByCoupon[target.CouponID], map[string]any{"target_type": target.TargetType, "target_id": target.TargetID})
	}
	flatItems := make([]map[string]any, 0, len(coupons))
	for _, coupon := range coupons {
		encoded, err := json.Marshal(coupon)
		if err != nil {
			return couponInternalError(c, err)
		}
		flat := map[string]any{}
		if err := json.Unmarshal(encoded, &flat); err != nil {
			return couponInternalError(c, err)
		}
		flat["targets"] = targetsByCoupon[coupon.ID]
		flatItems = append(flatItems, flat)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": flatItems, "counts": counts, "total": total, "page": page, "per_page": perPage,
		"impact": map[string]int64{"orders": orders, "discount_total": discountTotal, "revenue": revenue}})
}

func adminCouponCreate(c echo.Context) error {
	fields, err := decodeCouponObject(c, couponInputKeys())
	if err != nil {
		return couponBadRequest(c, "invalid JSON request")
	}
	coupon, targets, _, fieldErrors := couponCandidate(fields, core.Coupon{Status: "active", Scope: "all"}, true)
	if len(fieldErrors) != 0 {
		return couponInvalid(c, fieldErrors)
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := validateCouponTargets(tx, coupon, targets); err != nil {
			return err
		}
		if err := tx.Create(&coupon).Error; err != nil {
			return err
		}
		return replaceCouponTargets(tx, coupon.ID, targets)
	})
	if couponConflict(err) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "duplicate code"})
	}
	if err != nil {
		var validation *couponValidationError
		if errors.As(err, &validation) {
			return couponInvalid(c, validation.fields)
		}
		return couponInternalError(c, err)
	}
	return c.JSON(http.StatusOK, coupon)
}

func adminCouponGet(c echo.Context) error {
	id, err := couponID(c)
	if err != nil {
		return couponBadRequest(c, "invalid coupon id")
	}
	var coupon core.Coupon
	if err := data.DB.First(&coupon, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return couponInternalError(c, err)
	}
	targets, err := couponTargetViews(data.DB, coupon.ID)
	if err != nil {
		return couponInternalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"coupon": coupon, "targets": targets})
}

func adminCouponPatch(c echo.Context) error {
	id, err := couponID(c)
	if err != nil {
		return couponBadRequest(c, "invalid coupon id")
	}
	fields, err := decodeCouponObject(c, couponInputKeys())
	if err != nil {
		return couponBadRequest(c, "invalid JSON request")
	}
	var coupon core.Coupon
	var resultTargets []core.CouponTarget
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&coupon, id).Error; err != nil {
			return err
		}
		var existingTargets []core.CouponTarget
		if err := tx.Where("coupon_id = ?", coupon.ID).Order("id").Find(&existingTargets).Error; err != nil {
			return err
		}
		updated, targets, targetsProvided, fieldErrors := couponCandidate(fields, coupon, false)
		if len(fieldErrors) != 0 {
			return &couponValidationError{fields: fieldErrors}
		}
		used, err := couponHasHistory(tx, coupon)
		if err != nil {
			return err
		}
		if used {
			if len(fields) != 1 || fields["status"] == nil || updated.Status != "disabled" {
				return errCouponUsed
			}
			if err := tx.Model(&core.Coupon{}).Where("id = ?", coupon.ID).Update("status", "disabled").Error; err != nil {
				return err
			}
			coupon.Status = "disabled"
			resultTargets = existingTargets
			return nil
		}
		if !targetsProvided {
			targets = existingTargets
		}
		if updated.Scope == "all" && !targetsProvided {
			targets = nil
		}
		if err := validateCouponTargets(tx, updated, targets); err != nil {
			return err
		}
		if err := tx.Model(&core.Coupon{}).Where("id = ?", coupon.ID).Select("code", "label", "kind", "value", "buy_qty", "get_qty", "min_subtotal", "scope", "usage_limit", "per_user_limit", "starts_at", "ends_at", "stackable_with_free_shipping", "members_only", "bound_email", "status").Updates(&updated).Error; err != nil {
			return err
		}
		if targetsProvided || updated.Scope == "all" && len(existingTargets) > 0 {
			if err := replaceCouponTargets(tx, coupon.ID, targets); err != nil {
				return err
			}
		}
		coupon = updated
		resultTargets = targets
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if errors.Is(err, errCouponUsed) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "used coupons can only be disabled"})
	}
	var validation *couponValidationError
	if errors.As(err, &validation) {
		return couponInvalid(c, validation.fields)
	}
	if couponConflict(err) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "duplicate code"})
	}
	if err != nil {
		return couponInternalError(c, err)
	}
	return c.JSON(http.StatusOK, couponAdminResponse{Coupon: coupon, Targets: targetViewsFromRows(resultTargets)})
}

func targetViewsFromRows(targets []core.CouponTarget) []map[string]any {
	views := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		views = append(views, map[string]any{"target_type": target.TargetType, "target_id": target.TargetID})
	}
	return views
}

var errCouponUsed = errors.New("coupon already used")

type couponValidationError struct{ fields map[string]string }

func (e *couponValidationError) Error() string { return "invalid coupon" }

func adminCouponDelete(c echo.Context) error {
	id, err := couponID(c)
	if err != nil {
		return couponBadRequest(c, "invalid coupon id")
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var coupon core.Coupon
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&coupon, id).Error; err != nil {
			return err
		}
		used, err := couponHasHistory(tx, coupon)
		if err != nil {
			return err
		}
		if used {
			return errCouponUsed
		}
		return tx.Delete(&coupon).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if errors.Is(err, errCouponUsed) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "used coupon cannot be deleted"})
	}
	if err != nil {
		return couponInternalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func randomCouponCode(prefix string) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	var code strings.Builder
	code.Grow(len(prefix) + len(random))
	code.WriteString(prefix)
	for _, value := range random {
		code.WriteByte(alphabet[int(value)&31])
	}
	return code.String(), nil
}

func couponCodeExists(tx *gorm.DB, code string) (bool, error) {
	var count int64
	if err := tx.Model(&core.Coupon{}).Where("lower(code) = ?", strings.ToLower(code)).Count(&count).Error; err != nil {
		return false, err
	}
	return count != 0, nil
}

func adminCouponGenerate(c echo.Context) error {
	fields, err := decodeCouponObject(c, map[string]bool{"prefix": true, "size": true, "template": true})
	if err != nil {
		return couponBadRequest(c, "invalid JSON request")
	}
	prefix, _, err := couponField(fields, "prefix", "")
	if err != nil {
		return couponInvalid(c, map[string]string{"prefix": err.Error()})
	}
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if len(prefix) > 20 || (prefix != "" && !regexp.MustCompile(`^[A-Z0-9_-]+$`).MatchString(prefix)) {
		return couponInvalid(c, map[string]string{"prefix": "prefix must be at most 20 letters, numbers, underscores, or hyphens"})
	}
	size, ok, err := couponField(fields, "size", 0)
	if err != nil || !ok || size < 1 || size > 500 {
		return couponInvalid(c, map[string]string{"size": "size must be between 1 and 500"})
	}
	templateRaw, ok := fields["template"]
	if !ok || len(templateRaw) == 0 || string(templateRaw) == "null" {
		return couponInvalid(c, map[string]string{"template": "template is required"})
	}
	var templateFields map[string]json.RawMessage
	if err := json.Unmarshal(templateRaw, &templateFields); err != nil || templateFields == nil {
		return couponInvalid(c, map[string]string{"template": "template must be an object"})
	}
	if _, hasCode := templateFields["code"]; hasCode {
		return couponInvalid(c, map[string]string{"template": "template must not include a code"})
	}
	if _, hasStatus := templateFields["status"]; hasStatus {
		return couponInvalid(c, map[string]string{"template": "template must not include status"})
	}
	for key := range templateFields {
		if !couponInputKeys()[key] {
			return couponInvalid(c, map[string]string{"template": "template contains an unsupported field"})
		}
	}
	// A generated batch is one code per person, independent of client defaults.
	templateFields["usage_limit"] = json.RawMessage("1")
	templateFields["per_user_limit"] = json.RawMessage("1")
	encodedTemplate, err := json.Marshal(templateFields)
	if err != nil {
		return couponBadRequest(c, "invalid template")
	}
	templateFields["code"] = json.RawMessage(`"BATCH-CODE"`)
	seed := core.Coupon{Status: "active", Scope: "all"}
	template, targets, _, fieldErrors := couponCandidate(templateFields, seed, true)
	if len(fieldErrors) > 0 {
		return couponInvalid(c, fieldErrors)
	}
	codes := make([]string, 0, size)
	batch := core.CouponBatch{Prefix: prefix, Size: size, Template: encodedTemplate}
	if principal, err := auth.GetCurrentPrincipal(c); err == nil && principal != nil && principal.User != nil && principal.User.ID != 0 {
		creatorID := principal.User.ID
		batch.CreatedByID = &creatorID
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := validateCouponTargets(tx, template, targets); err != nil {
			return err
		}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		for i := 0; i < size; i++ {
			created := false
			for attempt := 0; attempt < 20; attempt++ {
				code, err := randomCouponCode(prefix)
				if err != nil {
					return err
				}
				exists, err := couponCodeExists(tx, code)
				if err != nil {
					return err
				}
				if exists {
					continue
				}
				coupon := template
				coupon.ID = 0
				coupon.Code = code
				coupon.Status = "active"
				coupon.BatchID = &batch.ID
				name := fmt.Sprintf("coupon_code_%d_%d", i, attempt)
				if err := tx.SavePoint(name).Error; err != nil {
					return err
				}
				if err := tx.Create(&coupon).Error; err != nil {
					if couponConflict(err) {
						if rollbackErr := tx.RollbackTo(name).Error; rollbackErr != nil {
							return rollbackErr
						}
						continue
					}
					return err
				}
				if err := replaceCouponTargets(tx, coupon.ID, append([]core.CouponTarget(nil), targets...)); err != nil {
					return err
				}
				codes = append(codes, code)
				created = true
				break
			}
			if !created {
				return errors.New("could not generate a unique coupon code")
			}
		}
		return nil
	})
	if errors.Is(err, errCouponUsed) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "coupon conflict"})
	}
	var validation *couponValidationError
	if errors.As(err, &validation) {
		return couponInvalid(c, validation.fields)
	}
	if err != nil {
		if couponConflict(err) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "duplicate code"})
		}
		return couponInternalError(c, err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"batch_id": batch.ID, "count": len(codes), "codes": codes})
}

func adminCouponPreview(c echo.Context) error {
	fields, err := decodeCouponObject(c, map[string]bool{"code": true, "cart": true})
	if err != nil {
		return couponBadRequest(c, "invalid JSON request")
	}
	code, _, err := couponField(fields, "code", "")
	if err != nil || strings.TrimSpace(code) == "" {
		return couponInvalid(c, map[string]string{"code": "code is required"})
	}
	cartRaw, ok := fields["cart"]
	if !ok {
		return couponInvalid(c, map[string]string{"cart": "cart is required"})
	}
	var cartFields map[string]json.RawMessage
	if err := json.Unmarshal(cartRaw, &cartFields); err != nil || cartFields == nil {
		return couponInvalid(c, map[string]string{"cart": "cart must be an object"})
	}
	for key := range cartFields {
		if key != "subtotal" && key != "qty" && key != "email" && key != "shipping" && key != "shipping_fee" {
			return couponInvalid(c, map[string]string{"cart": "cart contains an unsupported field"})
		}
	}
	var cartInput struct {
		Subtotal    int64  `json:"subtotal"`
		Qty         int    `json:"qty"`
		Email       string `json:"email"`
		Shipping    int64  `json:"shipping"`
		ShippingFee int64  `json:"shipping_fee"`
	}
	if err := json.Unmarshal(cartRaw, &cartInput); err != nil || cartInput.Subtotal < 0 || cartInput.Qty < 1 || cartInput.Qty > 10000 || cartInput.Shipping < 0 || cartInput.ShippingFee < 0 {
		return couponInvalid(c, map[string]string{"cart": "cart needs a nonnegative subtotal and shipping fee, and quantity from 1 to 10000"})
	}
	if cartInput.ShippingFee != 0 {
		cartInput.Shipping = cartInput.ShippingFee
	}
	var coupon core.Coupon
	if err := data.DB.Where("lower(code) = ?", strings.ToLower(strings.TrimSpace(code))).First(&coupon).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "invalid coupon"})
		}
		return couponInternalError(c, err)
	}
	var targets []core.CouponTarget
	if err := data.DB.Where("coupon_id = ?", coupon.ID).Find(&targets).Error; err != nil {
		return couponInternalError(c, err)
	}
	if coupon.Scope != "all" {
		return couponInvalid(c, map[string]string{"cart": "preview requires an all-products coupon because this form has no product selection"})
	}
	quote := core.CartQuote{Subtotal: cartInput.Subtotal, ShippingFee: cartInput.Shipping, Email: cartInput.Email}
	if coupon.Kind == "bogo" {
		base, remainder := cartInput.Subtotal/int64(cartInput.Qty), cartInput.Subtotal%int64(cartInput.Qty)
		for i := 0; i < cartInput.Qty; i++ {
			price := base
			if int64(i) < remainder {
				price++
			}
			quote.Lines = append(quote.Lines, core.PricedLine{Qty: 1, UnitPrice: price, LineTotal: price})
		}
	}
	discount, err := core.EvaluateWithTargets(quote, coupon, nil, targets)
	if err != nil {
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	}
	total := cartInput.Subtotal - discount.Amount
	if !discount.FreeShipping {
		if cartInput.Shipping > math.MaxInt64-total {
			return couponInvalid(c, map[string]string{"cart": "cart total is too large"})
		}
		total += cartInput.Shipping
	}
	return c.JSON(http.StatusOK, map[string]any{"amount": discount.Amount, "discount": discount.Amount, "free_shipping": discount.FreeShipping, "free_units": discount.FreeUnits, "label": discount.Label, "shipping": func() int64 {
		if discount.FreeShipping {
			return 0
		}
		return cartInput.Shipping
	}(), "total": total})
}
