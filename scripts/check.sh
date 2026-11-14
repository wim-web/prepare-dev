#!/usr/bin/env bash
# このリポジトリ自身の確認を一度に回す。
# 証明したモデルとコードの突き合わせと、一連の作業の確認には、検証器 Lean が要る。
set -euo pipefail
cd "$(dirname "$0")/.."
command -v lean >/dev/null 2>&1 || export PATH="$HOME/.elan/bin:$PATH"

echo "1. 整形と静的な検査"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "整形されていないファイルがある:"
  echo "$unformatted"
  exit 1
fi
go vet ./...

echo "2. テスト（証明したモデルとの突き合わせと、一連の作業の確認を含む）"
PPDEV_REQUIRE_LEAN=1 go test ./... "$@"

echo "3. ビルド"
go build -o bin/ppdev ./cmd/ppdev

echo "すべて通った。"
