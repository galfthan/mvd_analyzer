module github.com/mvd-analyzer/mvd-api

go 1.22

toolchain go1.25.0

require github.com/mvd-analyzer/mvd-analytics v0.0.0

require github.com/mvd-analyzer/mvd-reader v0.0.0 // indirect

replace (
	github.com/mvd-analyzer/mvd-analytics => ../mvd-analytics
	github.com/mvd-analyzer/mvd-reader => ../mvd-reader
)
