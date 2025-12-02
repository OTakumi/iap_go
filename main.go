package main

import (
	"fmt"
	"net/http/httputil"
)

func main() {
    // The final destination URL
    proxy := &httputil.ReverseProxy{Director: ...}

    // -----------------------------------------------------------------------------
	// Middleware Chain Construction
	// -----------------------------------------------------------------------------
	// IAPのHTTPリクエスト処理パイプラインを構築する
	// このチェーンは「Onion Architecture」になっている
	//
	// [処理フローと依存関係]
	// 1. Recovery: パニック時のサーバーダウンを防ぐ
	// 2. Logging: リクエストの開始/終了とステータスコードを記録する
	// 3. Auth: 【認証】未認証ユーザーをIdPへリダイレクトし、認証済みならContextにUserをセットする
	// 4. RBAC: 【認可】Context内のUser情報を元に、アクセス権限（Admin等）を判定する
	//    ※ Authより後にある必要がある（User情報が必要なため）
	// 5. HeaderInjection: バックエンド向けにヘッダーを整形（サニタイズ＆付与）する
	//    ※ RBACより後にある必要がある（権限確認済みのリクエストのみ通すため）
	//    ※ Proxyの直前にある必要がある（サニタイズ漏れを防ぐため）
	// 6. Proxy: リクエストを最終的にバックエンドへ転送する
	// -----------------------------------------------------------------------------
    handler := RecoverMiddleware(
        LoggingMiddleware(
            AuthMiddleware(
                // 認証成功時のみここへ到達
                // contextへは、user_id, email等が含まれていることが保証される
                RBACMiddleware(
                    // 権限チェック通過時のみここへ到達
                    HeaderInjectionMiddleware(
                        // バックエンドへの最終防衛線のため、偽装ヘッダーの削除と、正規ヘッダーの付与を行う
                        proxy,
                    ),
                ),
            ),
        ),
    )

    http.ListenAndServe(":8080", handler)
}
