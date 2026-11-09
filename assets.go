// Package preparedev embeds the files the prepare-dev binary ships: the agent skills,
// the guidance of the verification plugins and the built decision UI.
package preparedev

import (
	"embed"
	"io/fs"
)

//go:embed skills
var skillsFS embed.FS

//go:embed guidance
var guidanceFS embed.FS

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

// Guidance returns the guidance Markdown of a plugin.
func Guidance(name string) ([]byte, error) { return guidanceFS.ReadFile("guidance/" + name + ".md") }

// UIHTML is the single-file React decision UI built by Vite.
func UIHTML() []byte { return uiHTML }
