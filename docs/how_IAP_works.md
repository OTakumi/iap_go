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

```mermaid
sequenceDiagram
    autonumber
    actor User as User (Browser)
    participant IAP as IAP (Go App)
    participant IdP as IdP (Google/Auth0)
    participant Backend as Backend Service

    %% 1. 初回アクセス
    Note over User, IAP: 1. 初回アクセス (未認証)
    User->>IAP: GET https://internal.com/dashboard
    activate IAP
    
    Note right of IAP: [Middleware]<br/>Cookieを確認: セッションなし
    IAP->>IAP: State (CSRF対策) と Nonceを生成<br/>元のURLを一時保存

    IAP-->>User: 302 Redirect to IdP Auth URL<br/>(client_id, scope, redirect_uri, state)
    deactivate IAP

    %% 2. IdP認証
    Note over User, IdP: 2. IdPでの認証
    User->>IdP: 認証画面へアクセス / ログイン
    IdP-->>User: 302 Redirect to IAP Callback URL<br/>(code, state)

    %% 3. コールバック処理
    Note over User, IAP: 3. コールバック処理 (Go Handler)
    User->>IAP: GET /oauth2/callback?code=xxx&state=yyy
    activate IAP

    Note right of IAP: [Callback Handler]<br/>1. State検証<br/>2. "code" 抽出

    IAP->>IdP: POST /token (Code Exchange)
    activate IdP
    Note right of IAP: Back-channel通信
    IdP-->>IAP: 200 OK (ID Token, Access Token)
    deactivate IdP

    IAP->>IAP: ID Token検証 (署名, iss, exp)<br/>ユーザー情報抽出

    IAP->>IAP: セッションCookie生成 (暗号化)

    IAP-->>User: 302 Redirect to original URL (/dashboard)<br/>Set-Cookie: IAP_SESSION=...
    deactivate IAP

    %% 4. 認証後のアクセス
    Note over User, IAP: 4. 認証後の再アクセス
    User->>IAP: GET /dashboard (with Cookie)
    activate IAP
    Note right of IAP: Cookie有効確認 -> Backendへ
    IAP->>Backend: Proxy Request
    deactivate IAP
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

```mermaid
sequenceDiagram
    autonumber
    actor User as User (Browser)
    box "IAP Server (Go)" #F0F8FF
        participant MW as Middleware Chain
        participant Proxy as Reverse Proxy<br/>(httputil)
    end
    participant Backend as Backend Service

    %% 1. リクエスト受信
    Note over User, MW: 1. リクエスト受信
    User->>MW: GET /admin/dashboard<br/>(Cookie: IAP_SESSION=...)
    activate MW

    Note right of MW: **Authentication**<br/>Cookie復号・署名検証<br/>有効期限チェック

    alt セッション無効 / 期限切れ
        MW-->>User: 302 Redirect to Login
    else セッション有効
        Note right of MW: Contextにユーザー情報注入
        
        %% 2. 認可
        Note right of MW: **Authorization (RBAC)**<br/>ContextからUser取得<br/>パス権限チェック

        alt 権限なし (Forbidden)
            MW-->>User: 403 Forbidden
        else 権限あり
            %% 3. ヘッダー処理
            Note right of MW: **Header Injection**<br/>1. 既存X-Authヘッダー削除(Sanitize)<br/>2. 正しい情報を付与

            MW->>Proxy: ServeHTTP(w, r)
            activate Proxy

            %% 4. バックエンド転送
            Note over Proxy, Backend: 4. バックエンド転送
            Proxy->>Backend: Forward Request<br/>(X-Auth-Email: bob@example.com)
            activate Backend
            
            Note right of Backend: アプリ側でヘッダー参照
            Backend-->>Proxy: 200 OK Response
            deactivate Backend

            Proxy-->>User: Response
            deactivate Proxy
        end
    end
    deactivate MW
```

### 参考リンク

[Identity-Aware Proxy の TCP 転送とは？](https://dev.classmethod.jp/articles/identity-aware-proxy-tcp-forwarding/)
