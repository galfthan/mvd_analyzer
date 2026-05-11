module github.com/mvd-analyzer/qw-web

go 1.22

require (
	github.com/mvd-analyzer/qwanalytics v0.0.0
	github.com/mvd-analyzer/qwdemo v0.0.0
)

replace (
	github.com/mvd-analyzer/qwanalytics => ../qwanalytics
	github.com/mvd-analyzer/qwdemo => ../qwdemo
)
