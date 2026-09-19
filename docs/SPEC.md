# 仕様書

`aws-account-provisioner` の設計ドキュメント。

## 1. 概要

**Control Tower Account Factory** によるAWSアカウント作成と、**IAM Identity Center** の許可セット割り当て（グループ／ユーザー）を行う、単一バイナリのCLIツール。

AWSアカウントの作成には20〜40分かかり、かつ取り消しができない。本ツールはこの制約を前提に設計している。すなわち、**作成前にすべてを検証し、複数アカウントを並列に作成し、利用可能になったものから順に割り当てを適用する**。

## 2. スコープ

### 対象

- 1つのTSVファイルから複数アカウントを作成する
- 参照しているOU・許可セット・グループ・ユーザーの実在を、**何かを作成する前に**すべて検証する
- 作成状況を並列にポーリングし、進捗を表示する
- 各アカウントが利用可能になった時点で許可セットを割り当てる
- AWS上に常駐リソースを残さない（ローカル実行のCLIとして完結させる）

### 対象外

- Permissions Boundary の enforce 適用 / StackSet 操作
- SCP の管理
- アカウントの削除・停止・OU間の移動
- 既存アカウントの名称変更
- ルートメールアドレスの重複検出（AWSに該当APIが存在しないため。§9参照）

## 3. 前提条件

| 項目 | 内容 |
|---|---|
| Go | 1.23以降 |
| AWS Control Tower | 有効化済みで、Account Factory が Service Catalog 製品として存在すること |
| IAM Identity Center | 管理アカウントで有効化済みであること |
| AWSプロファイル | Organizations管理アカウントのロールをAssumeRoleできるSSOプロファイル |

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
| `ASSUME_ROLE_NAME` | 任意 | 管理アカウントで引き受けるロール名。既定値 `AWSControlTowerExecution` |
| `EMAIL_TEMPLATE` | 任意 | ウィザードがルートメールを導出するためのテンプレート。既定値 `aws+{account_name}@example.com` |
| `AWS_PROFILE` | 任意 | `--profile` で上書きされる |

`EMAIL_TEMPLATE` は `{account_name}` プレースホルダに対応し、小文字化したアカウント名で置換される。

## 7. 処理フロー

```
認証 → 検出 → 検証 → 作成 → ポーリング → 割り当て → 結果出力
```

1. **認証** — 指定プロファイルで `ROOT_ACCOUNT_ID` の `ASSUME_ROLE_NAME` をAssumeRole
2. **検出** — Account Factory 製品（Service Catalog）と Identity Center インスタンスを特定。許可セットをページネーションで取得し、各ARNを名前に解決して**キャッシュ**。ユーザー・グループ・OUの一覧も取得する
3. **検証** — TSVの形式を検査したうえで、参照しているOU・許可セット・グループ・ユーザーの実在を確認
4. **作成** — アカウントごとに `ProvisionProduct` を一括送信
5. **ポーリング** — アカウント1件につきgoroutine 1本、30秒間隔、上限60分
6. **割り当て** — `AVAILABLE` になったアカウントから順に割り当てを作成し、各要求が `SUCCEEDED` になるまで待機
7. **結果出力** — サマリを標準出力に表示し、`results/YYYYMMDD_HHMMSS_result.tsv` を書き出す

`--dry-run` はステップ2と3のみを実行する。

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
| `ErrorMessage` | 失敗時のみ |

サマリは標準出力にも表示する。

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

AssumeRole先のロールに最低限必要な権限。

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
