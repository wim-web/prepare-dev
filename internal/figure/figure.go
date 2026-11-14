// Package figure は、検証したモデルを実行して得た動きから図を作り、
// 図の箱・矢印と、元の動きとの対応を持つ。
package figure

import (
	"fmt"
	"sort"
	"strings"
)

// Edge は、ある状態から別の状態への動きひとつ。
type Edge struct {
	From   string `json:"from"`
	Label  string `json:"label"`
	Effect string `json:"effect,omitempty"`
	To     string `json:"to"`
}

func (e Edge) key() string { return e.From + "\x00" + e.Label + "\x00" + e.Effect + "\x00" + e.To }

// Mapping は、図の番号と、元の状態・動きとの対応。
type Mapping struct {
	Nodes map[string]string `json:"nodes"` // 番号 → 状態
	Edges map[string]Edge   `json:"edges"` // 番号 → 動き
}

// ParseExport は、モデルの書き出しから、調べた範囲と動きを取り出す。
func ParseExport(out string) (bound string, edges []Edge, err error) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		switch f[0] {
		case "BOUND":
			if len(f) >= 2 {
				bound = f[1]
			}
		case "EDGE":
			if len(f) != 5 {
				return "", nil, fmt.Errorf("動きの行が読めない: %q", line)
			}
			edges = append(edges, Edge{From: f[1], Label: f[2], Effect: f[3], To: f[4]})
		}
	}
	if len(edges) == 0 {
		return "", nil, fmt.Errorf("モデルから動きがひとつも出ていない")
	}
	return bound, edges, nil
}

func number(id string) int {
	n := 0
	fmt.Sscanf(id[1:], "%d", &n)
	return n
}

// Assign は、箱と矢印に番号を振る。
// 前の版の図と同じ状態・同じ動きには同じ番号を使い、新しいものには続きの番号を振る。
func Assign(edges []Edge, prev *Mapping) Mapping {
	m := Mapping{Nodes: map[string]string{}, Edges: map[string]Edge{}}
	nodeID, edgeID := map[string]string{}, map[string]string{}
	nextNode, nextEdge := 1, 1
	if prev != nil {
		for id, name := range prev.Nodes {
			nodeID[name] = id
			nextNode = max(nextNode, number(id)+1)
		}
		for id, e := range prev.Edges {
			edgeID[e.key()] = id
			nextEdge = max(nextEdge, number(id)+1)
		}
	}
	node := func(name string) {
		id, ok := nodeID[name]
		if !ok {
			id = fmt.Sprintf("N%d", nextNode)
			nextNode++
			nodeID[name] = id
		}
		m.Nodes[id] = name
	}
	for _, e := range edges {
		node(e.From)
		node(e.To)
		id, ok := edgeID[e.key()]
		if !ok {
			id = fmt.Sprintf("E%d", nextEdge)
			nextEdge++
			edgeID[e.key()] = id
		}
		m.Edges[id] = e
	}
	return m
}

func sortedIDs[T any](m map[string]T) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return number(ids[i]) < number(ids[j]) })
	return ids
}

func quote(s string) string { return strings.ReplaceAll(s, `"`, "'") }

// Mermaid は図の本体を作る。状態は色ではなく、番号と言葉で区別する。
func Mermaid(m Mapping) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	nodeID := map[string]string{}
	for _, id := range sortedIDs(m.Nodes) {
		nodeID[m.Nodes[id]] = id
		fmt.Fprintf(&b, "    %s[\"%s %s\"]\n", id, id, quote(m.Nodes[id]))
	}
	for _, id := range sortedIDs(m.Edges) {
		e := m.Edges[id]
		label := id + " " + e.Label
		if e.Effect != "" {
			label += "：" + e.Effect
		}
		fmt.Fprintf(&b, "    %s -->|\"%s\"| %s\n", nodeID[e.From], quote(label), nodeID[e.To])
	}
	return b.String()
}

// Meta は、図がいつ、何から作られたか。
type Meta struct {
	ID          string
	Version     int
	Title       string
	Verified    bool // 受理された検証結果の保存内容から作ったか
	GeneratedAt string
	Source      string   // 元にした形式条件と版
	SourceHash  string   // 元にした内容の印
	Evidence    string   // 使った検証結果
	Guarantee   string   // その検証結果から言えること
	Check       string   // 求める保証と照合した結果（図を作った時点）
	Bound       string   // 調べた範囲
	Unchecked   []string // 確かめていないこと
}

// Page は、図と、その読み方を一枚の文書にする。
func Page(meta Meta, m Mapping) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s 版%d：%s\n\n", meta.ID, meta.Version, meta.Title)
	if meta.Verified {
		b.WriteString("- 種類：検証対象から作った図\n")
	} else {
		b.WriteString("- 種類：説明図（検証結果とは結び付いていない）\n")
	}
	fmt.Fprintf(&b, "- 作った時点：%s\n", meta.GeneratedAt)
	fmt.Fprintf(&b, "- 元にした内容：%s（内容の印 %s）\n", meta.Source, short(meta.SourceHash))
	if meta.Evidence != "" {
		fmt.Fprintf(&b, "- 使った検証結果：%s\n", meta.Evidence)
	}
	if meta.Guarantee != "" {
		fmt.Fprintf(&b, "- 検証結果から言えること：%s\n", meta.Guarantee)
	}
	if meta.Check != "" {
		fmt.Fprintf(&b, "- 求める保証との照合：%s\n", meta.Check)
	}
	if meta.Bound != "" {
		fmt.Fprintf(&b, "- 図にした範囲：%s\n", meta.Bound)
	}
	b.WriteString("\nこの図は、作った時点の内容についてのものです。その後の変更は反映していません。\n\n")
	b.WriteString("```mermaid\n" + Mermaid(m) + "```\n\n")

	b.WriteString("## 箱（状態）\n\n| 番号 | 状態 |\n|---|---|\n")
	for _, id := range sortedIDs(m.Nodes) {
		fmt.Fprintf(&b, "| %s | %s |\n", id, m.Nodes[id])
	}
	b.WriteString("\n## 矢印（動き）\n\n| 番号 | どこから | 出来事 | 起きること | どこへ |\n|---|---|---|---|---|\n")
	for _, id := range sortedIDs(m.Edges) {
		e := m.Edges[id]
		effect := e.Effect
		if effect == "" {
			effect = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", id, e.From, e.Label, effect, e.To)
	}
	if len(meta.Unchecked) > 0 {
		b.WriteString("\n## 確かめていないこと\n\n")
		for _, u := range meta.Unchecked {
			fmt.Fprintf(&b, "- %s\n", u)
		}
	}
	b.WriteString("\n## 指摘の返し方\n\n箱や矢印の番号を添えてください。例：「E3 について：取り消したあともここを通るのか」\n")
	return b.String()
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
