// Package preparedev は、ppdev の実行ファイルに入れて配るファイルを持つ。今は skill だけ。
package preparedev

import (
	"embed"
	"io/fs"
)

//go:embed skills
var skillsFS embed.FS

// Skills は、実行ファイルに入っている skill を返す。skill ごとに一つのフォルダ。
func Skills() fs.FS {
	sub, err := fs.Sub(skillsFS, "skills")
	if err != nil {
		panic(err)
	}
	return sub
}
