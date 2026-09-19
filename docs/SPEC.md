# 仕様書

`aws-account-provisioner` の設計ドキュメント。

## 1. 概要

**Control Tower Account Factory** によるAWSアカウント作成と、**IAM Identity Center** の許可セット割り当て（グループ／ユーザー）を行う、単一バイナリのCLIツール。

AWSアカウントの作成には20〜40分かかり、かつ取り消しができない。本ツールはこの制約を前提に設計している。すなわち、**作成前にすべてを検証し、複数アカウントを並列に作成し、利用可能になったものから順に割り当てを適用する**。

## 2. スコープ

- 1つのTSVファイルから複数アカウントを作成する
- 参照しているOU・許可セット・グループ・ユーザーの実在を、**何かを作成する前に**すべて検証する
- 作成状況を並列にポーリングし、進捗を表示する
- 各アカウントが利用可能になった時点で許可セットを割り当てる
- AWS上に常駐リソースを残さない（ローカル実行のCLIとして完結させる）

## 3. 前提条件

| 項目 | 内容 |
|---|---|
| Go | 1.23以降 |
| AWS Control Tower | 有効化済みで、Account Factory が Service Catalog 製品として存在すること |
| IAM Identity Center | 管理アカウントで有効化済みであること |
| AWSプロファイル | Organizations管理アカウントに到達できるプロファイル（直接、または踏み台ロール経由。§6.1参照） |
| リージョン | プロファイルまたは `AWS_REGION` で解決されるリージョンが、**Control Tower のホームリージョン**であること。Account Factory はそこに存在するため |

## 4. コマンド

```bash
# 対話ウィザード — input.tsv を生成
aws-account-provisioner --init

# 検証のみ（何も作成しない）
aws-account-provisioner --dry-run input.tsv

# 実行
aws-account-provisioner input.tsv
```

| フラグ | 既定値 | 説明 |
|---|---|---|
| `--init` | `false` | 対話ウィザードを起動し `input.tsv` を書き出す |
| `--dry-run` | `false` | 入力とAWS側の状態を検証する。何も作成しない |
| `--profile` | `$AWS_PROFILE` | 管理アカウントへの接続に使うAWSプロファイル |
| `--yes` | `false` | 作成前の確認プロンプトを省略する（CI等の非対話実行向け） |

## 5. 入力ファイル

TSV形式、1行 = 1割り当て。`AccountEmail` が同一の行はグループ化され、アカウント作成は1回のみ、割り当てはその後に行数分実行される。

```tsv
AccountEmail	AccountName	OU	PrincipalType	PrincipalName	PermissionSetName
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	GROUP	Developers	AdministratorAccess
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	USER	taro	ReadOnlyAccess
aws+stg@example.com	stg-account	Staging (ou-xxxx-xxxxxxxx)	GROUP	Developers	AdministratorAccess
```

| 列 | 説明 |
|---|---|
| `AccountEmail` | ルートメールアドレス。AWS全体で一意である必要がある |
| `AccountName` | アカウント表示名 |
| `OU` | 作成先の組織単位。`名前 (ou-xxxx-xxxxxxxx)` 形式 |
| `PrincipalType` | `GROUP` または `USER` |
| `PrincipalName` | IAM Identity Center 上のグループ名またはユーザー名 |
| `PermissionSetName` | 許可セット名 |

## 6. 設定

環境変数から読み込む。`.env` ファイルにも対応する。

| 変数 | 必須 | 説明 |
|---|---|---|
| `ROOT_ACCOUNT_ID` | 必須 | Organizations管理アカウントのID（12桁） |
| `ASSUME_ROLE_NAME` | 任意 | 管理アカウントで引き受けるロール名。**未設定ならAssumeRoleを行わない**（§6.1参照） |
| `EMAIL_TEMPLATE` | 任意 | ウィザードがルートメールを導出するためのテンプレート。既定値 `aws+{account_name}@example.com` |
| `SSO_USER_FIRST_NAME` | 任意 | Account Factory が各アカウントに作る初期 Identity Center ユーザーの名。既定値 `Admin` |
| `SSO_USER_LAST_NAME` | 任意 | 同じく姓。既定値 `User` |
| `AWS_PROFILE` | 任意 | `--profile` で上書きされる |

`EMAIL_TEMPLATE` は `{account_name}` プレースホルダに対応し、小文字化したアカウント名で置換される。

### 6.1 認証モード

`ASSUME_ROLE_NAME` の有無で挙動が変わる。

| `ASSUME_ROLE_NAME` | 挙動 | 想定するケース |
|---|---|---|
| 未設定（既定） | AssumeRoleを行わず、プロファイルの認証情報をそのまま使う | プロファイルが管理アカウントを直接指している |
| 設定あり | `arn:aws:iam::{ROOT_ACCOUNT_ID}:role/{ASSUME_ROLE_NAME}` をAssumeRoleし、得た一時認証情報で以降のAPIを実行する | 踏み台ロールを経由する運用 |

いずれのモードでも、起動時に `sts:GetCallerIdentity` を呼び、**実際に到達しているアカウントが `ROOT_ACCOUNT_ID` と一致するかを検証する**。一致しない場合は何もせず中断する。プロファイルの取り違えによる誤ったアカウントへの操作を防ぐためのガードである。

AssumeRole時の `RoleSessionName` は `aws-account-provisioner` を用いる。CloudTrail上でツール起因の操作を識別できるようにするため。

## 7. 処理フロー

```
認証 → 検出 → 検証 → 作成 → ポーリング → 割り当て → 結果出力
```

1. **認証** — プロファイルを読み込み、`ASSUME_ROLE_NAME` があればAssumeRole（§6.1）。続けて `sts:GetCallerIdentity` で到達先アカウントが `ROOT_ACCOUNT_ID` と一致することを確認
2. **検出** — Account Factory 製品（Service Catalog）と Identity Center インスタンスを特定。許可セットをページネーションで取得し、各ARNを名前に解決して**キャッシュ**。ユーザー・グループ・OUの一覧も取得する
3. **検証** — TSVの形式を検査したうえで、参照しているOU・許可セット・グループ・ユーザーの実在を確認
4. **確認** — 作成対象を表示し、取り消せない操作であることを明示して確認を取る（`--yes` で省略可）
5. **作成** — アカウントごとに `ProvisionProduct` を一括送信
6. **ポーリング** — アカウント1件につきgoroutine 1本、30秒間隔、上限60分
7. **割り当て** — `AVAILABLE` になったアカウントから順に割り当てを作成し、各要求が `SUCCEEDED` になるまで待機
8. **結果出力** — サマリを標準出力に表示し、`results/YYYYMMDD_HHMMSS_result.tsv` を書き出す

`--dry-run` はステップ2と3のみを実行する。

### Account Factory に渡すパラメータ

| キー | 値 |
|---|---|
| `AccountEmail` | 入力ファイルの `AccountEmail` |
| `AccountName` | 入力ファイルの `AccountName` |
| `ManagedOrganizationalUnit` | `OU名 (ou-xxxx-xxxxxxxx)` |
| `SSOUserEmail` | アカウントのルートメールと同じ |
| `SSOUserFirstName` / `SSOUserLastName` | `SSO_USER_FIRST_NAME` / `SSO_USER_LAST_NAME` |

製品バージョン（プロビジョニングアーティファクト）は、`ListProvisioningArtifacts` が返すもののうち**最も新しい有効なもの**を使う。Control Tower は Account Factory を随時更新し、無効なバージョンでの起動は失敗するため。

### 許可セットをキャッシュする理由

`ListPermissionSets` はARNしか返さないため、名前を得るには各ARNに対して `DescribePermissionSet` を呼ぶ必要がある。参照のたびに解決すると許可セット数だけAPIコールが発生するため、検出フェーズで一度だけマッピングを構築し、メモリ上に保持する。

## 8. 出力

```
results/
└── 20260919_181530_result.tsv
```

| 列 | 説明 |
|---|---|
| `OU` | 作成先OU |
| `AccountID` | 作成されたアカウントID |
| `AccountName` | アカウント表示名 |
| `AccountEmail` | ルートメールアドレス |
| `Status` | `SUCCEEDED` / `FAILED` |
| `ErrorMessage` | 失敗時、および割り当てに失敗した場合 |

アカウントは作成できたが割り当てに失敗した場合、`Status` は `SUCCEEDED` のまま `ErrorMessage` に理由を記録する。アカウントは実在するため失敗としては扱わない。標準出力では `PARTIAL` として区別して表示する。

サマリは標準出力にも表示する。結果ファイルは実行ごとにタイムスタンプ付きで作られ、過去の実行結果を上書きしない。

## 9. エラーハンドリング

| ケース | 挙動 |
|---|---|
| 検証エラー | アカウントを1つも作らずに中断し、**問題を一括で列挙**する |
| 特定アカウントの作成失敗 | 他のアカウントは継続。失敗は記録する |
| 割り当ての失敗 | アカウント作成自体は成功として扱い、割り当てエラーを併記する |
| ポーリングのタイムアウト（60分） | `FAILED` として記録。AWS側では完了する可能性があるため、判明していればアカウントIDも出力する |
| `SIGINT` | ポーリングを停止して正常終了する。**送信済みの `ProvisionProduct` はAWS側で処理が継続する**ため、その旨を明示的に警告する |

ルートメールの重複は事前に検出できない。AWSがアカウント作成のドライランを提供していないため、重複はステップ4で失敗として顕在化する。

## 10. 必要なIAM権限

必要な権限は認証モード（§6.1）によって置き場所が変わる。

**AssumeRoleしない場合（既定）**
プロファイルの権限で全APIを実行するため、プロファイル自身が下表のアクションを持つ必要がある。

**AssumeRoleする場合**
権限は2層に分かれる。

| 層 | 対象 | 必要なもの |
|---|---|---|
| 1 | 実行者のプロファイル | `ROOT_ACCOUNT_ID` の `ASSUME_ROLE_NAME` に対する `sts:AssumeRole` のみ |
| 2 | 管理アカウント側のロール | 下表のアクション |

APIコールはすべて層2のロールの権限で実行されるため、プロファイル自身に Organizations や Identity Center の権限は不要になる。

なお `sts:GetCallerIdentity` はどちらのモードでも必要だが、明示的な許可なしに常に呼び出せる。

| サービス | アクション |
|---|---|
| `organizations` | `ListRoots`, `ListOrganizationalUnitsForParent`, `DescribeAccount` |
| `servicecatalog` | `SearchProducts`, `DescribeProduct`, `ProvisionProduct`, `DescribeRecord` |
| `sso`（ssoadmin） | `ListInstances`, `ListPermissionSets`, `DescribePermissionSet`, `CreateAccountAssignment`, `DescribeAccountAssignmentCreationStatus` |
| `identitystore` | `ListUsers`, `ListGroups`, `GetUserId`, `GetGroupId` |
| `sts` | `AssumeRole` |

すぐに使えるポリシードキュメントを `docs/iam-policy.json` として同梱する。

## 11. 実装

| 項目 | 選定 |
|---|---|
| 言語 | Go 1.23以降 |
| AWS | AWS SDK for Go v2（`organizations`, `servicecatalog`, `ssoadmin`, `identitystore`, `sts`, `config`） |
| CLIパース | 標準ライブラリ `flag` |
| 対話UI | `promptui`（長い一覧にはインクリメンタル検索を付与） |
| 設定 | `godotenv` |
| テスト | 標準ライブラリ `testing` |
| Lint | `golangci-lint`（CIで実行） |

### プロジェクト構成

```
.
├── main.go              # エントリポイント（フラグ解析とモード振り分け）
├── internal/
│   ├── config/          # 環境変数・.env の読み込み
│   ├── awsauth/         # 認証、AssumeRole、到達先アカウントの検証
│   ├── inputfile/       # TSV の読み書きと形式検証
│   ├── discovery/       # Account Factory / Identity Center の検出とキャッシュ
│   ├── provisioner/     # アカウント作成と並列ポーリング
│   ├── assigner/        # 許可セットの割り当て
│   ├── wizard/          # 対話ウィザード（--init）
│   └── report/          # サマリ表示と結果 TSV 出力
├── docs/
│   ├── SPEC.md
│   └── iam-policy.json  # AssumeRole 先ロール用のポリシー例
└── .github/workflows/ci.yml
```

`main.go` をモジュールルートに置くことで、`go install github.com/kawaguchi1035/aws-account-provisioner@latest` がそのままバイナリ名 `aws-account-provisioner` を生成する。`internal/` 配下のパッケージは各機能の実装時に追加する。
