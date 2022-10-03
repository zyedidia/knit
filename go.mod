module github.com/zyedidia/knit

go 1.19

require (
	github.com/adrg/xdg v0.4.0
	github.com/gobwas/glob v0.2.3
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/pelletier/go-toml/v2 v2.0.3
	github.com/schollz/progressbar/v3 v3.9.0
	github.com/segmentio/fasthash v1.0.3
	github.com/spf13/pflag v1.0.5
	github.com/zyedidia/gopher-lua v0.0.0-20220824000833-910ae8da2c12
	github.com/zyedidia/gopher-luar v0.0.0-20220811182431-9d2fc6a3867f
)

require (
	github.com/mattn/go-runewidth v0.0.13 // indirect
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	golang.org/x/crypto v0.0.0-20220131195533-30dcbda58838 // indirect
	golang.org/x/sys v0.0.0-20220128215802-99c3d69c2c27 // indirect
	golang.org/x/term v0.0.0-20210927222741-03fcf44c2211 // indirect
)

replace github.com/zyedidia/gopher-lua => ../gopher-lua
