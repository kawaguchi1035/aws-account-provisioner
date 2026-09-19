# aws-account-provisioner

**Control Tower Account Factory** によるAWSアカウント作成と、**IAM Identity Center** の許可セット割り当てを、1コマンドで実行するCLIツールです。

> ⚠️ **仕様どおりの機能は揃っていますが、実環境での検証はまだ行っていません。**
> 必ず `--dry-run` で確認してから実行してください。

## 背景

もともとAWSアカウントの払い出しは、マネジメントコンソールを画面操作して行っていました。しかし運用を重ねるうちに、いくつかの課題が見えてきました。

**繰り返し操作が多い**
ルートメールアドレスの命名規則を毎回手で組み立て、割り当てるユーザーや許可セットも同じ組み合わせを何度も選び直す。定型作業であるにもかかわらず、手作業ゆえに表記ゆれや取り違えが起こり得ます。

**手順が2つに分かれている**
アカウントを作って終わりではなく、そのあとIAM Identity Centerでユーザーやグループに許可セットを割り当てる作業が別途必要でした。Account Factoryによるアカウント作成には20〜40分かかるため、完了を待ってから次の画面へ移る、という待ち時間の分断も発生します。

**やり直しが効かない**
AWSアカウントの作成は取り消せません。許可セット名のタイプミスのような小さな誤りも、アカウントが出来上がってから発覚します。

これらを解決するために作ったのがこのツールです。**作成前にすべて検証し、複数アカウントを並列で作成し、出来上がった順に割り当てまで完了させる**ことを設計の軸にしています。

## 特徴

- **事前検証（`--dry-run`）** — 入力ファイルの形式と、参照しているOU・許可セット・グループ・ユーザーの実在をすべて確認します。アカウントは1つも作りません
- **一括作成** — 1つのTSVから複数アカウントを作成し、goroutineで並列にポーリングしながら進捗を表示します
- **割り当てまで一気通貫** — 許可セットの割り当てを別作業にせず、各アカウントが利用可能になった時点で順次適用します
- **対話ウィザード（`--init`）** — アカウント名の入力、OUの選択、割り当ての指定を対話形式で行い、入力ファイルを生成します
- **デプロイ不要** — ローカル実行のバイナリです。AWS上に常駐リソースを一切残しません

## インストール

```bash
go install github.com/kawaguchi1035/aws-account-provisioner@latest
```

## 使い方

```bash
# 1. 対話ウィザードで入力ファイルを生成
aws-account-provisioner --init

# 2. アカウントを作らずに検証だけ行う
aws-account-provisioner --dry-run input.tsv

# 3. 実行（確認プロンプトが出ます。--yes で省略可）
aws-account-provisioner input.tsv
```

### 入力ファイル

TSV形式で、1行 = 1割り当てです。同じメールアドレスの行はまとめられ、アカウント作成は1回だけ実行されます。

```tsv
AccountEmail	AccountName	OU	PrincipalType	PrincipalName	PermissionSetName
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	GROUP	Developers	AdministratorAccess
aws+dev@example.com	dev-account	Sandbox (ou-xxxx-xxxxxxxx)	USER	taro	ReadOnlyAccess
```

### 設定

環境変数、または `.env` ファイルで指定します。

| 変数 | 必須 | 既定値 | 説明 |
|---|---|---|---|
| `ROOT_ACCOUNT_ID` | 必須 | — | Organizations管理アカウントのID（12桁） |
| `ASSUME_ROLE_NAME` | 任意 | — | 設定するとこのロールをAssumeRoleする。未設定ならプロファイルの認証情報をそのまま使う |
| `EMAIL_TEMPLATE` | 任意 | `aws+{account_name}@example.com` | `--init` が使うルートメールのテンプレート |
| `SSO_USER_FIRST_NAME` | 任意 | `Admin` | Account Factory が作る初期 Identity Center ユーザーの名 |
| `SSO_USER_LAST_NAME` | 任意 | `User` | 同じく姓 |
| `AWS_PROFILE` | 任意 | — | `--profile` で上書き可能 |

#### 認証モード

多くの場合、**管理アカウントを指すプロファイルをそのまま渡すだけで動きます**（`ASSUME_ROLE_NAME` は不要）。

踏み台ロールを経由する運用では `ASSUME_ROLE_NAME` を設定してください。`arn:aws:iam::{ROOT_ACCOUNT_ID}:role/{ASSUME_ROLE_NAME}` をAssumeRoleし、以降のAPIをそのロールの権限で実行します。

どちらのモードでも起動時に `sts:GetCallerIdentity` で到達先アカウントを確認し、`ROOT_ACCOUNT_ID` と一致しなければ何もせず中断します。プロファイルの取り違えを防ぐためのガードです。

## 動作要件

- Go 1.23以降
- AWS Control Tower が有効化され、Account Factory が利用可能であること
- IAM Identity Center が有効化されていること
- 管理アカウントに到達できるAWSプロファイル（直接、または踏み台ロール経由。[必要な権限](docs/SPEC.md#10-必要なiam権限)を参照）

## 注意事項

- **アカウント作成は取り消せません。** 必ず `--dry-run` を先に実行してください
- **ルートメールの重複は事前検証できません。** AWSに該当するAPIが存在しないため、重複は作成時に判明し、失敗として報告されます
- **`Ctrl+C` はポーリングを止めるだけです。** 送信済みの `ProvisionProduct` リクエストはAWS側で処理が継続します。その場合はツールが明示的に警告を表示するので、コンソールで状態を確認してから再実行してください

## 実行結果

各アカウントの結果は `results/YYYYMMDD_HHMMSS_result.tsv` に記録されます。実行に数時間かかることもあり、標準出力は流れてしまうためです。

割り当てに失敗したアカウントは `PARTIAL` として表示されます。**アカウント自体は作成されている**ので、失敗した割り当てだけをコンソールで補ってください。

## ドキュメント

- [docs/SPEC.md](docs/SPEC.md) — 仕様書

## ライセンス

[MIT](LICENSE)
