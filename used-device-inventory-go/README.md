# used-device-inventory-go

中古スマホ・PC の個体管理アプリ（used-device-inventory）の Go 版です。
Python 版と同じデータベース・同じ写真フォルダをそのまま使い、
画面の構成・文面・業務ルールも Python 版に合わせてあります。
違いと切替の手順は [SWITCHOVER.md](SWITCHOVER.md) を見てください。

## 動かし方

単一の実行ファイルだけで動きます（CGO なし、依存は SQLite ドライバのみ）。

```
inventory serve                 # http://127.0.0.1:5000/ で起動
inventory serve --addr 100.64.0.5:5000   # Tailscale のアドレスで起動
```

- データ一式は、実行ファイルと同じ場所の `instance/` フォルダに入ります
  （`devices.db`、`secret_key`、`photos/`）。初回起動時に自動で作られます。
- 最初のアクセスで管理者アカウントの作成画面（/setup）が出ます。
  Python 版で使っていた `instance/` を引き継ぐ場合は、そのまま
  今までのユーザーでログインできます（[SWITCHOVER.md](SWITCHOVER.md)）。
- 待ち受けアドレスの決め方（優先順）: `--addr` フラグ ＞ 環境変数
  `DEVICE_ADDR` ＞ 実行ファイルと同じ場所の `config.txt` の `addr=` 行
  ＞ 既定の `127.0.0.1:5000`。
- データベースの場所は環境変数 `DEVICE_DB` で変えられます
  （並行運用で Python 版の `instance/` を指すときに使います）。

## ホスト側のコマンド

Web 画面とは別に、ホスト PC 上でだけ実行できるコマンドがあります。

```
inventory list-users                  # ユーザー一覧（* が管理者）
inventory reset-password <ユーザー名>  # パスワードの作り直し
```

- パスワードは実行時に聞かれます（コマンドラインには書きません）。
- データベースが無い場所では、空の DB を勝手に作らずエラーで終わります。
- 作り直すと、そのユーザーのログイン中セッションは無効になります
  （Python 版との意図的な違いです）。

## バックアップ

`instance/` フォルダを丸ごとコピーすれば完了です。データベースは
単一ファイル（WAL は使いません）で、写真は `instance/photos/` にあります。
ホスト以外の場所にも定期的に取ってください。

## 開発

```
go test ./...        # 全テスト
go build ./cmd/inventory
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/inventory.exe ./cmd/inventory
```

Python 版との相互運用チェック（同じ `instance/` を両版が交互に使う）:

```
../used-device-inventory/.venv/bin/python tools/interop_check.py
```
