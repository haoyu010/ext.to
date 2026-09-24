module github.com/haoyu010/ext.to

go 1.26.0

require (
	// gojianfan supplies the traditional-to-simplified character table used to
	// fold release names onto the simplified names TMDB indexes. It is a
	// single map with no dependencies of its own and is released into the
	// public domain.
	github.com/siongui/gojianfan v0.0.0-20210926212422-2f175ac615de
	golang.org/x/net v0.59.0
	gopkg.in/yaml.v3 v3.0.1
)
