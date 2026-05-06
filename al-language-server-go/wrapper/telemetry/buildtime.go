package telemetry

// BuildTimeConnString is the App Insights connection string baked at
// release-build time via -ldflags:
//
//	go build -ldflags "-X .../telemetry.BuildTimeConnString=IngestionEndpoint=...;InstrumentationKey=..."
//
// Empty at build time = no-op telemetry compiled in (kill switch). The
// wrapper falls back to AL_LSP_APPINSIGHTS_CONNSTR_OVERRIDE env var for
// runtime override (used by QA / dev).
var BuildTimeConnString = ""
