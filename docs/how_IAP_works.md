# Identity_Aware Proxyの仕組み

## IAPの主な機能

### コンテナ

- IAPとアプリケーションの通信を中継するサーバーでオンプレミスやIaaS側へ配置される
- この機能による社内リソースへの安全な通信、アクセスが可能になる

### エージェント

- エンドユーザーの端末にインストールされる
- ブラウザベースでは対応しきれないHTTPS以外のプロトコルでアクセスできるようになる

### IAM連携

- アプリケーション利用の認証、認可を認証基盤(IAM)によって行う仕組み
- 既設の認証基盤を利用することが可能となる

### デバイスポスチャ

- 接続端末のリスクを判断する機能
- 端末の状態を判断しアクセスを許可するかを判断する
- EDRと連携する機能を持つものもある

## リクエスト処理の流れ

### 未認証時のフロー(初回アクセス, セッション切れ)

ユーザーが初めてアクセスしてきた場合、IAPはIdP(Google Auth0, Oktaなど)を使って認証を行う

1. リクエスト受信, セッション確認
    - ユーザーが特定のURLへアクセスする
    - IAPはCookieを確認し、セッションID(またはJWT)が存在しない、あるいは無効であることを検知する
2. OIDC認証リクエストの構築(State生成)
    - IAPはCSRF対策のためのランダムな文字列(state)と、認証後の戻り先URL(nonceやリダイレクトURL)を生成し、
    Cookieやメモリ内のキャッシュに保存する
3. IdPへのリダイレクト
    - IAPはユーザーをIdPの認証画面へリダイレクトさせる
4. ユーザー認証, コールバック
    - ユーザーがIdPでログインを完了すると、IdPはIAPのコールバックURL（例: /oauth2/callback）へリダイレクトする
    - この際、code(認可コード)とstateが付与される
5. Token Exchange, 検証
    - IAPのコールバックハンドラが起動する
    - State検証
        - リクエストに含まれるstateが、2.で保存したものと一致するか確認する(CSRF対策)
    - トークン交換
        - code を使ってIdPのトークンエンドポイントを叩き、ID Token（とAccess Token）を取得する
6. セッション確率
    - 検証済みのユーザー情報（Email, Subなど）を元に、IAP独自のセッションCookieを発行し、ブラウザにセットする
    - ユーザーを本来のアクセス先へリダイレクトする

```plantuml
@startuml
title IAP Authentication Flow (OIDC Authorization Code Flow)
autonumber

actor "User (Browser)" as User
participant "IAP (Go App)" as IAP
participant "IdP (Google/Auth0 etc)" as IdP
participant "Backend Service" as Backend

== 1. 初回アクセス (未認証) ==
User -> IAP: GET https://internal.com/dashboard
activate IAP

note right of IAP
  [Middleware]
  Cookieを確認: セッションなし
end note

IAP -> IAP: State (CSRF対策) と Nonceを生成
IAP -> IAP: 元のURL (/dashboard) をCookie等に一時保存

IAP --> User: 302 Redirect to IdP Auth URL\n(client_id, scope, redirect_uri, state)
deactivate IAP

== 2. IdPでの認証 ==
User -> IdP: 認証画面へアクセス
User -> IdP: ログイン (ユーザー名/パスワード)
IdP --> User: 302 Redirect to IAP Callback URL\n(code, state)

== 3. コールバック処理 (Go Handler) ==
User -> IAP: GET /oauth2/callback?code=xxx&state=yyy
activate IAP

note right of IAP
  [Callback Handler]
  1. Stateの検証 (Cookieと比較)
  2. "code" を取り出す
end note

IAP -> IdP: POST /token (Code Exchange)
note right of IAP
  Back-channel通信
  (Goのoauth2ライブラリ等を使用)
end note
activate IdP
IdP --> IAP: 200 OK (ID Token, Access Token)
deactivate IdP

IAP -> IAP: ID Tokenの検証 (署名, iss, exp)
IAP -> IAP: ユーザー情報の抽出 (email, sub)

IAP -> IAP: セッションCookieの生成 (暗号化推奨)

IAP --> User: 302 Redirect to original URL (/dashboard)\nSet-Cookie: IAP_SESSION=...
deactivate IAP

== 4. 認証後の再アクセス ==
User -> IAP: GET /dashboard (with IAP_SESSION Cookie)
activate IAP

note right of IAP
  [Middleware]
  Cookieを確認: 有効
  Contextにユーザー情報をセット
end note

IAP -> Backend: リクエストをプロキシ (X-Auth-Emailヘッダ付与)
activate Backend
Backend --> IAP: レスポンス
deactivate Backend

IAP --> User: レスポンス
deactivate IAP

@enduml
```

### 認証済みのフロー(通常アクセス)

すでにログイン済みのユーザーからのリクエスト処理

1. リクエスト受信(Middleware)
    - リクエストヘッダーのCookieからセッションの情報を読み取る
2. セッションの検証
    - セッションが改ざんされていないか、有効期限以内かを確認する
3. 認可(Authorization)チェック
4. ヘッダーインジェクション
    - バックエンドアプリが「誰がアクセスしているか」を把握するため、特定のリクエストヘッダーを付与する
    - 一般的に使われるヘッダー
      - X-Auth-Request-User: emailアドレス
      - X-Auth-Request-Email: emailアドレス
      - X-Auth-Request-Sub: ユーザーの一意なID
    - セキュリティ注意点
      - クライアントから偽装された同名のヘッダーが送られてくる可能性があるため、プロキシする前にこれらのヘッダーを一度削除(Sanitize)してから、IAPが付与しなおす必要がある
5. バックエンドへのプロキシ(Reverse Proxy)
    - リクエストをバックエンドサーバーへ転送する
    - バックエンドからのレスポンスを受け取り、そのままクライアントへ返す

```plantuml
@startuml
title IAP Authenticated Access Flow (Proxy)
autonumber

actor "User (Browser)" as User
box "IAP Server (Go)" #F0F8FF
    participant "Middleware Chain" as MW
    participant "Reverse Proxy\n(httputil)" as Proxy
end box
participant "Backend Service" as Backend

== 1. リクエスト受信 ==
User -> MW: GET /admin/dashboard (Cookie: IAP_SESSION=...)
activate MW

note right of MW
  **1. セッション検証 (Authentication)**
  - Cookieの復号・署名検証
  - 有効期限 (exp) の確認
end note

alt セッション無効 / 期限切れ
    MW --> User: 302 Redirect to Login (OIDC Flow)
else セッション有効
    note right of MW
      **Contextへの注入**
      ctx = context.WithValue(r.Context(), "user", userInfo)
    end note
end

== 2. 認可 (Authorization) ==
note right of MW
  **2. 権限チェック (RBAC)**
  - ContextからUser取得
  - パス/メソッドに対する権限確認
  (例: user="bob" は "/admin" にアクセス可能か?)
end note

alt 権限なし (Forbidden)
    MW --> User: 403 Forbidden
else 権限あり
    == 3. ヘッダー処理 (Header Injection) ==
    note right of MW
      **3. ヘッダーのサニタイズと付与**
      - 既存の X-Auth-* ヘッダーを削除 (偽装防止)
      - 正しい情報を付与
        X-Auth-Email: bob@example.com
        X-Auth-Sub: user_12345
    end note

    MW -> Proxy: ServeHTTP(w, r)
    activate Proxy
    
    == 4. バックエンド転送 ==
    Proxy -> Backend: Forward Request\n(with X-Auth Headers)
    activate Backend
    
    note right of Backend
      **アプリ側の処理**
      リクエストヘッダーを見て
      「誰からのアクセスか」を判断
    end note

    Backend --> Proxy: 200 OK Response
    deactivate Backend

    Proxy --> User: Response
    deactivate Proxy
end
deactivate MW

@enduml
```

### 参考リンク

[Identity-Aware Proxy の TCP 転送とは？](https://dev.classmethod.jp/articles/identity-aware-proxy-tcp-forwarding/)
