package i18n

import (
	"encoding/json"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

var Bundle *i18n.Bundle

func Init() {
	Bundle = i18n.NewBundle(language.English)
	Bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	Bundle.LoadMessageFile("web/locales/en.json")
	Bundle.LoadMessageFile("web/locales/zh-TW.json")
}

func GetLocalizer(lang string) *i18n.Localizer {
	return i18n.NewLocalizer(Bundle, lang)
}
