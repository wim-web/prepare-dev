package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// DirName は、プロジェクトの中で記録を置くフォルダの名前。
const DirName = ".ppdev"

// Store は、プロジェクトの .ppdev/ にある正式な記録。
type Store struct {
	Root string // プロジェクトの場所
	Dir  string // Root/prepare
	Now  func() time.Time
}

func at(root string) *Store {
	return &Store{Root: root, Dir: filepath.Join(root, DirName), Now: time.Now}
}

// Init は記録の置き場所を作る。既にあれば何もしない。
func Init(root string) (*Store, error) {
	s := at(root)
	if err := os.MkdirAll(filepath.Join(s.Dir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.logPath(), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return s, f.Close()
}

// Open は既にある記録を開く。
func Open(root string) (*Store, error) {
	s := at(root)
	if _, err := os.Stat(s.logPath()); err != nil {
		return nil, Errf("not-found", "%s に記録がない。先に ppdev init を実行する", s.Dir)
	}
	return s, nil
}

func (s *Store) logPath() string { return filepath.Join(s.Dir, "log.jsonl") }

// load はログを読み直して状態を作る。
// 途中で書き込みが切れた最後の行は確定していないものとして扱い、その手前までの長さを返す。
func (s *Store) load() (*State, int64, error) {
	data, err := os.ReadFile(s.logPath())
	if err != nil {
		return nil, 0, err
	}
	st := NewState()
	var valid int64
	line := 0
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break // 改行で終わっていない末尾は、確定していない
		}
		line++
		raw := data[:i]
		data = data[i+1:]
		if len(bytes.TrimSpace(raw)) > 0 {
			var tx Tx
			if err := json.Unmarshal(raw, &tx); err != nil {
				return nil, 0, fmt.Errorf("記録の %d 行目が読めない: %w", line, err)
			}
			if err := st.apply(&tx); err != nil {
				return nil, 0, fmt.Errorf("記録の %d 行目が決まりに合わない: %w", line, err)
			}
		}
		valid += int64(i + 1)
	}
	return st, valid, nil
}

// View は、今の状態を読むだけの処理を実行する。
func (s *Store) View(fn func(*State) error) error {
	st, _, err := s.load()
	if err != nil {
		return err
	}
	return fn(st)
}

func (s *Store) lock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Build は、今の状態を見て変更を組み立てる。
// 二つ目の値は、変更を当てたあとの状態から、呼び出し元へ返す結果を作る。
type Build func(st *State) ([]Op, func(*State) any, error)

// Update は変更をひとつの確定単位として記録する。
//
// 決まりに合わない変更は何も書かずに誤りを返す。同じ reqID の要求が既に確定していれば、
// 何も書かずに前回の結果を返す（二つ目の値が true）。
func (s *Store) Update(reqID string, build Build) (json.RawMessage, bool, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	st, valid, err := s.load()
	if err != nil {
		return nil, false, err
	}
	if reqID != "" {
		if prev, ok := st.Seen[reqID]; ok {
			return prev, true, nil
		}
	} else {
		reqID = newID()
	}
	ops, finish, err := build(st)
	if err != nil {
		return nil, false, err
	}
	// 読み直したときと同じ形にしてから当てる。そうしないと、内容の印が保存の前後で変わる。
	if ops, err = normalize(ops); err != nil {
		return nil, false, err
	}
	tx := Tx{ID: reqID, At: s.Now().UTC().Format(time.RFC3339Nano), Ops: ops}
	if err := st.apply(&tx); err != nil {
		return nil, false, err
	}
	var result any = map[string]any{}
	if finish != nil {
		result = finish(st)
	}
	tx.Result, err = json.Marshal(result)
	if err != nil {
		return nil, false, err
	}
	line, err := json.Marshal(tx)
	if err != nil {
		return nil, false, err
	}
	if err := s.append(valid, append(line, '\n')); err != nil {
		return nil, false, fmt.Errorf("記録を保存できなかった。この変更は確定していない: %w", err)
	}
	return tx.Result, false, nil
}

func normalize(ops []Op) ([]Op, error) {
	b, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	var out []Op
	return out, json.Unmarshal(b, &out)
}

func (s *Store) append(valid int64, line []byte) error {
	f, err := os.OpenFile(s.logPath(), os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	// 途中で切れた末尾があれば捨ててから、続きに書く。
	if err := f.Truncate(valid); err != nil {
		f.Close()
		return err
	}
	if _, err := f.WriteAt(line, valid); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Applied は、ある要求が確定しているかを返す。保存できたか分からないときの照合に使う。
func (s *Store) Applied(reqID string) (json.RawMessage, bool, error) {
	var res json.RawMessage
	var ok bool
	err := s.View(func(st *State) error {
		res, ok = st.Seen[reqID]
		return nil
	})
	return res, ok, err
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "tx-" + hex.EncodeToString(b)
}

// PutBlob は、読んだ内容をそのまま保存し、内容の印を返す。
func (s *Store) PutBlob(data []byte) (string, error) {
	h := HashBytes(data)
	p := s.blobPath(h)
	if _, err := os.Stat(p); err == nil {
		return h, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp-" + newID()
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return h, os.Rename(tmp, p)
}

// GetBlob は、保存した内容を印から取り出す。
func (s *Store) GetBlob(hash string) ([]byte, error) {
	if len(hash) < 3 {
		return nil, Errf("not-found", "保存した内容がない: %s", hash)
	}
	b, err := os.ReadFile(s.blobPath(hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, Errf("not-found", "保存した内容がない: %s", hash)
	}
	return b, err
}

func (s *Store) blobPath(hash string) string {
	return filepath.Join(s.Dir, "blobs", hash[:2], hash[2:])
}

// Snapshot は、ファイルを読んで保存し、道筋と内容の印を返す。
func (s *Store) Snapshot(paths []string) ([]FileRef, error) {
	var out []FileRef
	for _, p := range paths {
		full := p
		if !filepath.IsAbs(p) {
			full = filepath.Join(s.Root, p)
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil, Errf("not-found", "読むファイルがない: %s", p)
		}
		h, err := s.PutBlob(b)
		if err != nil {
			return nil, err
		}
		out = append(out, FileRef{Path: p, Hash: h})
	}
	return out, nil
}

// FileHash は、ファイルの今の内容の印を返す。読めなければ空。
func (s *Store) FileHash(p string) string {
	full := p
	if !filepath.IsAbs(p) {
		full = filepath.Join(s.Root, p)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return ""
	}
	return HashBytes(b)
}
