module github.com/mvd-analyzer/mvd-web

go 1.22

require (
	github.com/mvd-analyzer/mvd-analytics v0.0.0
	github.com/mvd-analyzer/mvd-reader v0.0.0
)

replace (
	github.com/mvd-analyzer/mvd-analytics => ../mvd-analytics
	github.com/mvd-analyzer/mvd-reader => ../mvd-reader
)
