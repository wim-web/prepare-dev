// Package preparedev embeds the files the prepare-dev binary ships: the agent skills
// (the guidance of the verification plugins is in the references of the prepare-verify
// skill) and the built decision UI.
package preparedev

import (
	"embed"
	"io/fs"
)

//go:embed skills
var skillsFS embed.FS

//go:embed ui/build/index.html
var uiHTML []byte

// Skills is the skills tree (one directory per skill).
func Skills() fs.FS {
	sub, err := fs.Sub(skillsFS, "skills")
	if err != nil {
		panic(err)
	}
	return sub
}

// UIHTML is the single-file React decision UI built by Vite.
func UIHTML() []byte { return uiHTML }
