# YAMAP 自動いいねツール (YAMAP Auto Liker)

Go言語と `chromedp` を使用して、YAMAPのタイムラインや活動記録を自動的に巡回し、未リアクションの投稿に「いいね！」（絵文字リアクション）を送信するツールです。

## 機能

- **自動ログイン:** YAMAPに自動でログインします。
- **タイムライン巡回 (`react-timeline`):** フォローしているユーザーのタイムラインを巡回し、まだリアクションしていない投稿に「いいね！」します。
- **活動記録一覧巡回 (`react-activities`):** 特定のユーザー（自分など）の活動記録一覧ページを巡回し、まだリアクシしていない投稿に「いいね！」します。
- **ヘッドレスブラウザ実行:** Google Chrome (Chromium) をヘッドレスモードで操作するため、画面を表示せずにバックグラウンドで実行可能です。

## 必要要件

- Go 1.18 以上
- Google Chrome または Chromium ブラウザ

## セットアップ

1.  **リポジトリのクローン:**
    ```bash
    git clone <repository_url>
    cd <repository_directory>
    ```

2.  **依存関係のインストール:**
    ```bash
    go mod tidy
    ```

3.  **環境変数の設定:**
    `.env` ファイルを作成し、以下の変数を設定します（Jules環境では自動設定される場合があります）。
    ```
    YAMAP_EMAIL="your_email@example.com"
    YAMAP_PASSWORD="your_password"
    TIMELINE_POST_COUNT_TO_PROCESS=50
    ACTIVITIES_POST_COUNT_TO_PROCESS=30
    ```

## 使い方

### タイムラインへのいいね

フォローしているユーザーのタイムラインを巡回します。

```bash
go run main.go -action react-timeline
```

### 活動記録一覧へのいいね

（主に自分の）活動記録一覧ページを巡回します。

```bash
go run main.go -action react-activities
```

## アーキテクチャ

このツールは **モノリシック・インメモリセッション方式** を採用しています。
ログインからリアクション送信までの一連の処理を単一のプロセス内で完結させ、セッション情報（クッキー等）をメモリ上で保持することで、ファイル生成制限のある環境（サンドボックス等）でも安定して動作するように設計されています。

詳細は `docs/specifications.md` を参照してください。