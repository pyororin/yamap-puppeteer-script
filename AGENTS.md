# このエージェントについて

このエージェントは、YAMAPのタイムライン上の活動記録に自動で「いいね！」（絵文字リアクション）を送信すること、およびフォロワーのフォローバックを自動で行うことを目的としています。

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
    TIMELINE_POST_COUNT_TO_PROCESS=30
    ACTIVITIES_POST_COUNT_TO_PROCESS=100
    ```
    `TIMELINE_POST_COUNT_TO_PROCESS` はタイムラインで「いいね！」を送信する活動記録の目標件数です。
    `ACTIVITIES_POST_COUNT_TO_PROCESS` は活動一覧で「いいね！」を送信する活動記録の目標件数です。

3.  **依存関係のインストール:**
    Go Modulesを使用して、必要なパッケージをインストール・整理します。
    ```bash
    go mod tidy
    ```

3.  **スクリプトの実行:**
    ソースが複数ファイルに分かれているため、`go run main.go` ではなく `go run .` を使ってください。

    ```bash
    go run . -action react-timeline        # タイムラインの投稿へ「いいね！」
    go run . -action react-activities      # 活動一覧の投稿へ「いいね！」
    go run . -action follow-back           # フォロワーをフォローバック
    go run . -action list-non-mutual       # 片思いフォローを一覧化（読み取りのみ）
    go run . -action unfollow-non-mutual   # 片思いフォローを解除
    ```

    -   **react-timeline / react-activities**
        投稿ページを開いて絵文字リアクションを送ります。
        既に自分がリアクション済みの投稿はスキップします。
        同じ絵文字を再度クリックするとリアクションが**解除**されてしまうため、この判定は必須です。
        `-url <活動記録URL>` を付けると、その1件だけを処理します（動作確認用）。

    -   **follow-back**
        ログイン中のユーザーのフォロワーページを自動で特定して巡回し、
        「フォローされています」マークがあり且つ「フォローする」ボタンがあるユーザーをフォローバックします。
        ページネーションに対応しており、過去の全フォロワーを対象に処理を行います。
        既に「フォロー中」のユーザーや、おすすめ枠に表示されるユーザーはスキップします。

    -   **list-non-mutual / unfollow-non-mutual**
        `list-non-mutual` はフォロー中一覧を全ページ巡回し、
        こちらがフォローしていて相手がフォローしていないユーザーを `non_mutual_unique.txt` に書き出します。
        `unfollow-non-mutual` はそのファイルを入力にフォローを解除するため、**先に `list-non-mutual` の実行が必要**です。

## エージェントへの指示

-   **言語:** ユーザーへの返答、Gitのコミットメッセージなど、すべてのコミュニケーションは**日本語**で行ってください。

-   **依存関係の管理:**
    `go.mod` に変更を加えた場合や、依存関係に問題が発生した場合は、必ず `go mod tidy` コマンドを実行して `go.mod` と `go.sum` を最新の状態に保ってください。

-   **テスト:**
    コードに変更を加えた際は、`go test ./...` を実行して、変更が既存の機能に影響を与えていないことを確認してください。（注: 現在このプロジェクトにテストはありませんが、テストが追加された場合はこのステップは必須です。）

-   **デバッグとサイト仕様変更への対応:**

    **CSSクラス名を手がかりにしないでください。** YAMAPのフロントエンドはNuxt(Vue)からNext.jsへ刷新され、
    クラス名はEmotionのハッシュ（`css-1a2b3c`）になりました。ビルドのたびに変わるため、セレクタとして使えません。
    `window.__NUXT__` も存在しません（現在は `__NEXT_DATA__` / `__APOLLO_CLIENT__` ですが、
    Apolloのキャッシュは空でデータ取得には使えません）。

    現在依存しているセレクタは以下です。いずれも `aria-label` / `data-testid` / `href` を手がかりにしています。

    -   ログインフォーム (`input[name="email"]`, `input[name="password"]`)
    -   ログインボタン (XPath: `//button[span[text()='ログイン']]`)
    -   活動記録へのリンク (`a[href^="/activities/"]`)
    -   絵文字リアクションボタン (`button[aria-label="絵文字をおくる"]`)
    -   絵文字ピッカー (`[role="dialog"]`)、絵文字 (`button[aria-label="thumbs up"]` など)
    -   リアクション数 (`a[aria-label^="絵文字をおくった人"]`)
    -   リアクション済みの印 (`.viewer-has-reacted`) — **ピッカーを開いて初めて描画される**
    -   活動一覧のエントリ (`[data-testid="activity-entry"]`)
    -   フォロー／フォロワーのカード (`article`、ユーザー識別は `data-testid="user"`)
    -   ページネーション (`button[aria-label="次のページに移動する"]`)

    タブ名は `?tab=follows`（フォロー中）と `?tab=followers`（フォロワー）です。
    `followings` / `following` は404になります。
    **フォロー中一覧には「おすすめのユーザー」カードが毎ページ混在します。**
    自分のフォローかどうかは「フォロー中」ボタンの有無で判定してください。

    スクリプトが期待通りに動作しない場合は、調査用アクションを使ってください。
    一時的なデバッグコードを書く必要はありません。

    ```bash
    go run . -action debug-activity-page              # 活動記録ページの構造を調査
    go run . -action debug-activity-page -url <URL>   # 特定の投稿を調査
    go run . -action debug-follow-buttons             # フォロー系ページの構造を調査
    ```

-   **実行環境による差異:**
    GitHub Actions のランナーでは Chrome の初回起動が20秒を超えることがあり、
    chromedp の既定の `WSURLReadTimeout` では `websocket url timeout reached` で失敗します。
    アロケータのオプションで90秒に延長しているため、新しくブラウザを起動する処理を追加する場合も同様に設定してください。

-   **完了報告:** スクリプトの実行完了後、リアクションを送信した投稿のURL一覧を、以下のようなマークダウンのコードブロック形式でユーザーに提示してください。
    ```
    https://yamap.com/activities/xxxxxxxx
    https://yamap.com/activities/yyyyyyyy
    ```

-   **実行環境:**
    このスクリプトはブラウザ自動操作ライブラリ `chromedp` を利用しています。実行環境にはGoogle ChromeまたはChromiumがインストールされている必要があります。