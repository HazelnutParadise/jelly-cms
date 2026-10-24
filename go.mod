module github.com/HazelnutParadise/jelly-cms

go 1.27.3

require (
	github.com/fastschema/qjs v0.0.6
	github.com/goccy/go-json v0.11.2
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/gorilla/securecookie v1.1.2
	github.com/gorilla/sessions v1.4.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/labstack/echo/v4 v4.16.0
	github.com/labstack/gommon v0.5.0
	github.com/markbates/goth v1.82.0
	github.com/microcosm-cc/bluemonday v1.0.27
	github.com/nicksnyder/go-i18n/v2 v2.6.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/yuin/goldmark v1.8.6
	golang.org/x/crypto v0.58.0
	golang.org/x/image v0.47.0
	golang.org/x/net v0.61.0
	golang.org/x/oauth2 v0.37.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	gorm.io/driver/postgres v1.6.3
	gorm.io/gorm v1.31.2
)

require (
	cloud.google.com/go/compute/metadata v0.10.0 // indirect
	github.com/alecthomas/chroma/v2 v2.27.0 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/dlclark/regexp2/v2 v2.2.1 // indirect
	github.com/go-chi/chi/v5 v5.5.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-colorable v0.1.16 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	// wazero v1.12.0 fails QuickJS plugin startup with fastschema/qjs v0.0.6
	// ("jsFunctionProxy signature mismatch"). Keep v1.11.0 until qjs is updated.
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/time v0.16.0 // indirect
)
