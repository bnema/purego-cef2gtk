module github.com/bnema/purego-cef2gtk

go 1.26

require (
	github.com/bnema/purego v0.11.0-bnema.3
	github.com/bnema/purego-cef v0.13.1
	github.com/bnema/puregotk v0.6.0
	golang.org/x/sys v0.43.0
)

replace github.com/bnema/puregotk => ../puregotk

replace github.com/bnema/purego => ../purego

replace github.com/bnema/purego-cef => ../purego-cef
