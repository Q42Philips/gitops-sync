package config

import (
	"github.com/gobwas/glob"
)

type GlobValue struct {
	glob.Glob
	Separators []rune
	pattern    string
}

func (i *GlobValue) Set(pattern string) (err error) {
	// empty means default, glob := nil
	if pattern == "" {
		return
	}
	i.pattern = pattern
	i.Glob, err = glob.Compile(pattern, i.Separators...)
	return err
}

func (i *GlobValue) Get() interface{} { return i.Glob }
func (i *GlobValue) String() string   { return i.pattern }
