package markup

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ValidateWidgetParams validates supplied values and defaults against declarations.
func ValidateWidgetParams(spec WidgetSpec, input map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(input))
	for key := range input {
		if !widgetParamNameRE.MatchString(key) || reservedWidgetParam[key] {
			return nil, fmt.Errorf("invalid widget parameter name %q", key)
		}
		if _, ok := spec.Params[key]; !ok {
			return nil, fmt.Errorf("undeclared widget parameter %q", key)
		}
	}
	keys := make([]string, 0, len(spec.Params))
	for key := range spec.Params {
		if !widgetParamNameRE.MatchString(key) || reservedWidgetParam[key] {
			return nil, fmt.Errorf("invalid widget parameter name %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		constraint := spec.Params[key]
		value, supplied := input[key]
		if !supplied {
			if constraint.Default == nil {
				if constraint.Required {
					return nil, fmt.Errorf("missing widget parameter %q", key)
				}
				continue
			}
			value = fmt.Sprint(constraint.Default)
		}
		if strings.TrimSpace(value) != value || len(value) > paramMax(constraint.MaxLength) {
			return nil, fmt.Errorf("invalid widget parameter %q", key)
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("invalid widget parameter %q", key)
			}
		}
		var typed any
		switch constraint.Type {
		case "", "string":
			typed = value
		case "number":
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, fmt.Errorf("invalid number parameter %q", key)
			}
			typed = n
		case "boolean":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("invalid boolean parameter %q", key)
			}
			typed = b
		case "datetime":
			if _, err := time.Parse("2006-01-02", value); err != nil {
				if _, err = time.Parse(time.RFC3339, value); err != nil {
					return nil, fmt.Errorf("invalid datetime parameter %q", key)
				}
			}
			typed = value
		case "date":
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return nil, fmt.Errorf("invalid date parameter %q", key)
			}
			typed = value
		case "enum":
			allowed := false
			for _, candidate := range constraint.Values {
				if candidate == value {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf("invalid enum parameter %q", key)
			}
			typed = value
		default:
			return nil, fmt.Errorf("unsupported parameter type %q", constraint.Type)
		}
		out[key] = typed
	}
	return out, nil
}

func paramMax(n int) int {
	if n <= 0 || n > 2048 {
		return 256
	}
	return n
}

// DynamicItemsKey canonicalizes the query dimensions used by Expand.
func DynamicItemsKey(kind string, attrs map[string]string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	category := strings.TrimSpace(attrs["category"])
	limit := strings.TrimSpace(attrs["limit"])
	if limit == "" {
		limit = "20"
	}
	if n, err := strconv.Atoi(limit); err != nil || n < 1 || n > 100 {
		limit = "20"
	} else {
		limit = strconv.Itoa(n)
	}
	category = strings.ToLower(category)
	return kind + "|category=" + category + "|limit=" + limit
}

var dynamicCategoryRE = regexp.MustCompile(`^[\pL\pN_-]{1,80}$`)
var widgetParamNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var reservedWidgetParam = map[string]bool{"name": true, "version": true, "v": true}

func validDynamicCategory(category string) bool {
	return category == "" || dynamicCategoryRE.MatchString(category)
}

func widgetFrameURL(name string, spec WidgetSpec, attrs map[string]string) (string, error) {
	if !widgetNameRE.MatchString(name) || spec.Version < 1 {
		return "", fmt.Errorf("invalid widget identity")
	}
	values, err := ValidateWidgetParams(spec, attrs)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	for key, value := range values {
		query.Set(key, fmt.Sprint(value))
	}
	path := "/widgets/" + strings.ToLower(name) + "/" + strconv.Itoa(spec.Version) + "/frame"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path, nil
}
