module github.com/csmarshall/schrodeck

go 1.27

toolchain go1.27.1

require (
	github.com/csmarshall/schrodeck/deckformat v0.0.0
	howett.net/plist v1.0.1
)

replace github.com/csmarshall/schrodeck/deckformat => ./deckformat
