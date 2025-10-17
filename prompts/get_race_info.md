指定された日付 `${{ inputs.race_date }}` と開催場所 `${{ inputs.race_location }}` のレース情報を検索し、結果を要約してください。

実行するコマンドは `go run main.go -action get-race-info -date ${{ inputs.race_date }} -location ${{ inputs.race_location }}` です。

結果はマークダウン形式で、見つかったレースの名称、概要、距離、開催日をリストアップしてください。