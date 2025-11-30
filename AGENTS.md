# このエージェントについて

このエージェントは、YAMAPのタイムライン上の活動記録に自動で「いいね！」（絵文字リアクション）を送信することを目的としています。

## セットアップと実行方法


1.  **リポジトリのクローン:**
    ```bash
    git clone <repository_url>
    cd <repository_directory>
    ```

2.  **.envファイルの準備:**
    ルートディレクトリに `.env` ファイルが既に存在していることを確認してください。このファイルにはYAMAPのアカウント情報が設定されている必要があります。
    `.env` ファイルの中身:
    ```
    YAMAP_EMAIL="your_email@example.com"
    YAMAP_PASSWORD="your_password"
    TIMELINE_POST_COUNT_TO_PROCESS=50
    ACTIVITIES_POST_COUNT_TO_PROCESS=30
    ```
    `TIMELINE_POST_COUNT_TO_PROCESS` はタイムラインで「いいね！」を送信する活動記録の目標件数です。
    `ACTIVITIES_POST_COUNT_TO_PROCESS` は活動一覧で「いいね！」を送信する活動記録の目標件数です。

3.  **依存関係のインストール:**
    Go Modulesを使用して、必要なパッケージをインストール・整理します。
    ```bash
    go mod tidy
    ```

3.  **スクリプトの実行:**
    タイムラインへの「いいね！」機能を使うには、`-action react-timeline` フラグを付けてスクリプトを実行します。
    ```bash
    go run main.go -action react-timeline
    ```

## エージェントへの指示

-   **言語:** ユーザーへの返答、Gitのコミットメッセージなど、すべてのコミュニケーションは**日本語**で行ってください。

-   **依存関係の管理:**
    `go.mod` に変更を加えた場合や、依存関係に問題が発生した場合は、必ず `go mod tidy` コマンドを実行して `go.mod` と `go.sum` を最新の状態に保ってください。

-   **テスト:**
    コードに変更を加えた際は、`go test ./...` を実行して、変更が既存の機能に影響を与えていないことを確認してください。（注: 現在このプロジェクトにテストはありませんが、テストが追加された場合はこのステップは必須です。）

-   **デバッグとサイト仕様変更への対応:**
    YAMAPのウェブサイト構成は将来変更される可能性があります。特に、以下のHTML要素のセレクタは変更されやすい箇所です。
    -   ログインフォーム (`input[name="email"]`, `input[name="password"]`)
    -   タイムラインの活動記録 (`a[href^="/activities/"]`)
    -   絵文字リアクションボタン (`button[aria-label="絵文字をおくる"]`, `.emojiPickerBody`など)

    スクリプトが期待通りに動作しない場合、まずこれらのセレクタが最新のHTML構造と一致しているかを確認してください。**必要であれば、`main.go`にHTML構造を出力するような一時的なデバッグコードを追記して調査を行ってください。**

-   **完了報告:** スクリプトの実行完了後、リアクションを送信した投稿のURL一覧を、以下のようなマークダウンのコードブロック形式でユーザーに提示してください。
    ```
    https://yamap.com/activities/xxxxxxxx
    https://yamap.com/activities/yyyyyyyy
    ```

-   **実行環境:**
    このスクリプトはブラウザ自動操作ライブラリ `chromedp` を利用しています。実行環境にはGoogle ChromeまたはChromiumがインストールされている必要があります。