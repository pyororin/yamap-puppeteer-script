# YAMAP 自動リアクション・フォロー管理ツール (Go / chromedp)

Go と [chromedp](https://github.com/chromedp/chromedp) で Chrome を操作し、[YAMAP](https://yamap.com) 上の定型作業(絵文字リアクション、フォローバック、片思いフォローの整理)を自動化するコマンドラインツールです。GitHub Actions による定期実行用ワークフローも同梱しています。

> [!WARNING]
> 非公式の個人ツールです。利用は自己責任で、YAMAP の利用規約を必ず確認してください。詳細は「[注意事項](#注意事項)」を参照してください。

## 特長

- **タイムライン巡回 (`react-timeline`)**: フォロー中ユーザーのタイムラインを巡回し、未リアクションの活動記録に絵文字リアクションを送信
- **活動一覧巡回 (`react-activities`)**: 活動一覧ページを巡回して同様にリアクション。`-url` で1件だけ処理することも可能
- **フォローバック (`follow-back`)**: フォロワー一覧をページネーションごと巡回し、「フォローされています」かつ未フォローのユーザーをフォロー
- **片思いフォローの整理 (`list-non-mutual` / `unfollow-non-mutual`)**: 相互でないフォローを一覧化(`non_mutual_unique.txt` に出力)し、そのファイルを入力として解除
- **誤解除の防止**: 既にリアクション済みの投稿はスキップ(同じ絵文字の再クリックは解除になるため)
- **調査用アクション**: `debug-activity-page` / `debug-follow-buttons` でサイト構造変更時のセレクタ調査が可能
- **インメモリセッション**: ログインからリアクション送信までを1プロセス内で完結(詳細は [docs/specifications.md](docs/specifications.md))

## 技術スタック

| 区分 | 内容 |
| --- | --- |
| 言語 | Go 1.24.3(`go.mod`) |
| ブラウザ操作 | chromedp / cdproto |
| 設定読み込み | godotenv(`.env`) |
| CI | GitHub Actions(ubuntu-latest + Google Chrome) |

## 動作要件

- Go 1.24 以上
- Google Chrome または Chromium
- YAMAP アカウント

## セットアップ

```bash
git clone <このリポジトリのURL>
cd yamap-puppeteer-script
go mod tidy
```

プロジェクト直下に `.env` を作成し、下記の「[設定](#設定)」の変数を定義してください(`.env` は `.gitignore` 済みです)。
CI などでは環境変数から `.env` を生成する `generate_env.sh` も利用できます。

## 使い方

ソースが複数ファイルに分かれているため `go run main.go` ではなく `go run .` を使います。

```bash
go run . -action react-timeline        # タイムラインの投稿へリアクション
go run . -action react-activities      # 活動一覧の投稿へリアクション
go run . -action follow-back           # フォロワーをフォローバック
go run . -action list-non-mutual       # 片思いフォローを一覧化(読み取りのみ)
go run . -action unfollow-non-mutual   # 片思いフォローを解除(先に list-non-mutual が必要)
go run . -action react-activities -url https://yamap.com/activities/xxxxxxxx  # 1件のみ処理
```

| フラグ | 説明 |
| --- | --- |
| `-action` | 実行するアクション(上記のほか `debug-activity-page`, `debug-follow-buttons`) |
| `-url` | 対象の活動記録URL(`react-activities` / `debug-activity-page` で使用) |

## 設定

環境変数(または `.env`)で指定します。値はリポジトリに含めないでください。

| 名前 | 用途 |
| --- | --- |
| `YAMAP_EMAIL` | ログイン用メールアドレス |
| `YAMAP_PASSWORD` | ログイン用パスワード |
| `TIMELINE_POST_COUNT_TO_PROCESS` | `react-timeline` で処理する投稿の目標件数 |
| `ACTIVITIES_POST_COUNT_TO_PROCESS` | `react-activities` で処理する投稿の目標件数 |

## GitHub Actions

`.github/workflows/` に以下があります。認証情報はリポジトリの Secrets(`YAMAP_EMAIL`, `YAMAP_PASSWORD`)から渡します。

| ワークフロー | 内容 |
| --- | --- |
| `daily-yamap-like.yml` | タイムラインへのリアクション(手動実行 + 日次スケジュール) |
| `daily-yamap-activities-like.yml` | 活動一覧へのリアクション(手動実行 + スケジュール、`-url` 指定も可) |
| `daily-yamap-follow-back.yml` | フォローバック(スケジュール) |
| `verify-login.yml` | ログイン疎通確認 |

GitHub Actions のランナーでは Chrome の初回起動が遅いため、ウォームアップ実行と `WSURLReadTimeout` の延長(90秒)を行っています。

## 構成

```
.
├── main.go            # エントリポイント(フラグ解析、リアクション/フォローバック処理)
├── nonmutual.go       # 片思いフォローの一覧化・解除
├── debugpage.go       # ページ構造の調査用アクション
├── generate_env.sh    # 環境変数から .env を生成
├── docs/specifications.md  # 設計仕様
├── AGENTS.md          # 開発メモ(依存セレクタ、サイト構造の注意点)
└── .github/workflows/ # 定期実行・疎通確認
```

## 制限事項

- YAMAP のフロントエンド変更によりセレクタが無効になる可能性があります(`aria-label` / `data-testid` / `href` に依存)。動作しない場合は調査用アクションを使用してください。
- `docs/specifications.md` は旧フロントエンド(Nuxt)時代の記述を含みます。現行のセレクタは `AGENTS.md` を参照してください。
- テストコードはありません。

## 注意事項

- 本ツールは YAMAP の公式ツールではありません。ログインを伴う自動操作は **YAMAP の利用規約に抵触する可能性** があります。利用前に最新の規約を確認し、**自己責任** で使用してください。
- 本ツールの使用により生じたいかなる損害(アカウント制限・停止を含む)についても、作者は責任を負いません。
- サーバーへの負荷やアカウント制限を避けるため、**過度な頻度・件数での実行は避けて** ください。
- 認証情報(メールアドレス・パスワード)は `.env` や Secrets で管理し、決してコミットしないでください。
