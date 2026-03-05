module github.com/CampusTech/axm2snipe

go 1.26

require (
	github.com/michellepellon/go-snipeit v0.0.0-20250601021625-86633d87262f
	github.com/sirupsen/logrus v1.9.4
	github.com/zchee/abm v0.0.0-20260219125447-a33aa6475061
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.13.0 // indirect
)

replace github.com/michellepellon/go-snipeit => github.com/CampusTech/go-snipeit v0.0.0-20260305065845-b7bc2ae2b0e7

replace github.com/zchee/abm => github.com/CampusTech/abm v0.0.0-20260305072810-8df1f6a89c33
