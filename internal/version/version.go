// Package version exposes the build version and service name reported by the
// /info endpoint.
//
// Both values can be overridden at link time via -ldflags, e.g.:
//
//	go build -ldflags "-X github.com/thesouldev/goboxd/internal/version.Version=v1.2.3"
//	go build -ldflags "-X github.com/thesouldev/goboxd/internal/version.ServiceName=goboxd"
//
// Environment variables BUILD_VERSION and SERVICE_NAME (resolved by
// internal/config) take precedence over these compile-time defaults at run
// time.
package version

// Version is the build version reported by /info.
//
// The default "dev" applies when no link-time override is supplied. The
// production build pipeline injects a tagged version string via -ldflags.
var Version = "dev"

// ServiceName identifies the service in /info responses and structured logs.
var ServiceName = "goboxd"
