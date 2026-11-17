# fnOS 轻量对象存储（fnos-oss）

[![Release](https://img.shields.io/github/v/release/Contribuv/fnos-oss?label=release&color=3b7bff)](https://github.com/Contribuv/fnos-oss/releases)
[![Platform](https://img.shields.io/badge/platform-x86%20%7C%20arm-4d8dff)](#安装)
[![Go](https://img.shields.io/badge/go-1.26%2B-00add8?logo=go&logoColor=white)](https://go.dev/)
[![fnOS](https://img.shields.io/badge/fnOS-1.2.0%2B-1f2937)](#安装)
[![Downloads](https://img.shields.io/github/downloads/Contribuv/fnos-oss/total?label=downloads&color=33c38c)](https://github.com/Contribuv/fnos-oss/releases)

一个可安装到飞牛 fnOS 的单机 S3 兼容对象存储。后端是单个 Go 二进制，管理端内嵌在二进制中、通过飞牛统一网关访问；S3 API 使用独立 TCP 端口，供 rclone、AWS CLI、boto3 等客户端连接。

已提供开源的 [Typecho 对象存储插件](https://github.com/Contribuv/Typecho-fnos-oss)，可以把博客附件直接写入本服务；其他博客、图床或业务系统也可以按开发文档自行对接（标准的 S3 SigV4 + path-style 即可）。

## 功能

- S3 Signature V4：Header 签名与 Presigned URL（GET / PUT）
- `aws-chunked` 流式上传与分块验签
- 存储桶与对象 CRUD、ListObjects V1/V2、前缀/分隔符浏览
- Range 下载、CopyObject、DeleteObjects、CORS
- 分段上传：Create / UploadPart / Complete / Abort / ListParts / ListUploads
- 访问密钥管理（启用、停用、删除、最近使用时间）
- 访问密钥可按存储桶授权，支持只读密钥；未选桶表示全部桶
- 桶策略：`private` / `public-read`（公共读仅允许匿名下载）
- 管理端：概览、存储桶、访问密钥、连接示例、网络与安全、设置
- 管理端可将整个存储桶（或当前目录）流式打包为 zip 下载，无需安装客户端
- 管理端使用飞牛统一网关 + 管理员校验，不额外占用 TCP 端口
- 安装向导与“应用中心 → 应用设置”均可修改 S3 端口，保存后自动重启

## 客户端案例

- [Typecho-fnos-oss](https://github.com/Contribuv/Typecho-fnos-oss)：Typecho 附件上传插件，支持直链 / 预签名 / 代理、分段上传与失败回退本地。安装与配置见管理端「开发文档 → 客户端案例」。

除 Typecho 外，任何支持 S3 协议的应用都可以直接接入；自研博客、图床或后台系统可按「开发文档」中的签名、上传与预签名示例实现。

## 界面与访问方式

| 通道 | 地址 | 说明 |
| --- | --- | --- |
| 管理端 | `/app/fnos-oss` | 只能从飞牛桌面/飞牛统一网关进入，校验 `X-Trim-*` 登录态，仅管理员可用 |
| S3 API | `http://NAS_IP:<端口>` | 供 S3 客户端使用，采用 path-style：`http://NAS_IP:9100/桶名/对象键` |

> **重要：TCP 端口不经过飞牛登录校验。**
> 默认只应暴露在可信局域网。需要公网访问时，请自行使用 Lucky、frp 或 Nginx 反向代理到该端口，并启用 HTTPS、访问控制和来源 IP 限制；**不要在路由器上直接把该端口映射到公网**。

## 构建

依赖：

- Go 1.25+（bbolt v1.5.0 的最低要求）
- [fnpack](https://developer.fnnas.com/docs/cli/fnpack/) 1.2.x
- PowerShell 5.1+（Windows）或 bash（Linux/macOS）

Windows：

```powershell
.\build.ps1 -Fnpack C:\path\to\fnpack.exe
# 只构建 x86：.\build.ps1 -Fnpack ... -Arch amd64
# 只编译不打包：.\build.ps1 -SkipFpk
```

Linux / macOS：

```bash
FNPACK=/usr/local/bin/fnpack ./build.sh
# 只构建 x86：ARCHS="amd64:x86" ./build.sh
```

输出位于 `dist/`：

- `fnos-oss_1.0.20_x86.fpk` — 供 x86_64 设备安装
- `fnos-oss_1.0.20_arm.fpk` — 供 ARM64 设备安装

应用包含 Linux 原生二进制，飞牛要求 `manifest` 的 `platform` 与实际架构一致（只接受 `x86` / `arm` / `all`，其中 `all` 仅适用于不含架构相关二进制的包），因此同时支持两种设备的方式是**同一版本打两个包**：`pack/manifest` 保留模板值 `platform=x86`，构建脚本在每个架构的暂存目录里把它改写为对应架构，源文件本身不被修改。应用上架或发布时请同时上传两份包，或让 ARM 用户下载 `_arm.fpk`。

Windows 没有 POSIX 权限位，`fnpack` 会把包内文件打成 `0666`。两个构建脚本都会在打包后自动运行 `tools/fixfpk`，把 `cmd/*` 与 `app/fnososs` 修正为 `0755`、其余文件修正为 `0644`，因此在 Windows 上构建的包也可以直接安装，无需回到 Linux 重新打包。

## 安装

1. 在飞牛应用中心选择“手动安装”，上传对应架构的 `.fpk`。
2. 安装向导中填写 **S3 TCP 端口**（默认 `9100`，范围 `1024-65535`）。
3. 安装完成后，从飞牛桌面打开“轻量对象存储”图标。
4. 进入“访问密钥”页面，复制默认密钥，或为不同客户端新建独立密钥。
5. 在“连接配置”页面查看 Endpoint 与客户端示例。

### 修改端口

飞牛应用中心 → 轻量对象存储 → 应用设置 → 修改“S3 TCP 端口” → 保存。`config_callback` 会重写配置并重启服务，新端口立即生效。请同步更新客户端、反向代理和防火墙规则。

## 客户端配置

通用参数：

```text
Endpoint        http://NAS_IP:9100
Access Key      <管理端创建>
Secret Key      <管理端创建>
Region          us-east-1
Signature       AWS Signature V4
Addressing      Path style（必须）
```

rclone 示例：

```ini
[fnos-oss]
type = s3
provider = Other
access_key_id = <Access Key>
secret_access_key = <Secret Key>
endpoint = http://NAS_IP:9100
region = us-east-1
force_path_style = true
```

```bash
rclone lsd fnos-oss:
rclone copy ./data fnos-oss:mybucket/data
```

AWS CLI 示例：

```bash
export AWS_ACCESS_KEY_ID='<Access Key>'
export AWS_SECRET_ACCESS_KEY='<Secret Key>'
export AWS_DEFAULT_REGION='us-east-1'
aws --endpoint-url http://NAS_IP:9100 s3 ls
```

## 公网访问（Lucky 反向代理）

1. 准备独立域名（例如 `s3.example.com`）和 HTTPS 证书。
2. 在 Lucky 新建 Web 服务/反向代理：前端 `https://s3.example.com`，后端 `http://127.0.0.1:<S3端口>`。
3. 保留原始路径与 `Host` 头，调大请求体上限以支持大文件上传。
4. 视需要叠加来源 IP 限制或额外认证。管理端始终从飞牛桌面进入，不要通过反向代理暴露。
5. 回到“设置”页，把“公网 / 反向代理地址”填写为 `https://s3.example.com`，分享链接会使用该地址。

## 数据与备份

- 数据目录：`${TRIM_PKGVAR}/data`
  - `meta.db`：bbolt 元数据（桶、对象索引、密钥、设置）
  - `objects/`：对象内容，按对象键的 SHA-256 分片存放
  - `uploads/`、`tmp/`：分段上传与写入临时文件
- 备份建议：先停止应用，再整体复制数据目录；恢复时保持目录结构一致。
- 卸载会停止服务并删除应用文件；对象数据位于应用数据卷，请在卸载前自行确认是否需要备份。

### 迁移到另一台飞牛

`meta.db` 里的密钥是明文存储的，桶、密钥、对象索引都可以随数据目录整体搬走。绑定到飞牛文件夹的桶例外：它记录的是**旧机器的绝对路径**，需要在新机器上重新指向。

1. 旧机停止应用，打包 `TRIM_PKGVAR/data`（SSH：`sudo tar -C /volX/@appdata/fnos-oss -czf /tmp/oss-data.tgz data`）。
2. 新机安装同版本并启动一次，让它建好 `TRIM_PKGVAR`、`TRIM_PKGETC` 和专用应用用户。
3. 新机停止应用，把备份解压覆盖到 `TRIM_PKGVAR/data`，然后修正属主：
   ```bash
   sudo chown -R "$TRIM_USERNAME:$TRIM_GROUPNAME" "$TRIM_PKGVAR"
   ```
4. 启动应用，进入每个绑定目录的桶 → “存储位置”，改成新机器上的实际目录。只改指向，不移动文件。
5. 如果新机的卷路径与旧机一致（都是 `/vol1/...`），第 4 步通常可以跳过。

也可以完全不搬数据目录，改用 `rclone sync` 在新机器上重建：适合跨版本、跨架构或想顺便换布局的情况，代价是需要重新创建访问密钥。

## 开发调试

```bash
# 本地运行（Windows 无 Unix Socket，会跳过网关监听）
OSS_DEV=1 go run ./cmd/ossd \
  --config ./oss.json \
  --admin-addr 127.0.0.1:19101
```

- `OSS_DEV=1` 且请求来自 loopback 时，管理端视为开发管理员。
- 开发时访问 `http://127.0.0.1:19101/app/fnos-oss/`，与前端 `<base href="/app/fnos-oss/">` 保持一致。
- 相关环境变量：`OSS_PORT`、`OSS_SOCKET`、`OSS_DATA_DIR`、`OSS_GATEWAY_PREFIX`、`OSS_REGION`、`OSS_LOG_LEVEL`、`OSS_ADMIN_ADDR`。

测试：

```bash
go test ./...
```

## 已知限制

- 单节点存储，无副本、无纠删码、无多租户 IAM/ACL。
- 不支持对象版本控制、生命周期、对象锁、静态网站托管。
- 桶策略只支持 `private` 与 `public-read` 两种。
- 不内置 TLS；公网请通过 Lucky / Nginx 等终止 HTTPS。
- 分段上传的分段大小、最大对象大小受“设置 → 上传限制”约束。
- 管理端依赖飞牛统一网关注入的登录态 Header；直接访问 Socket 会被拒绝。

## 目录结构

```text
cmd/ossd/            服务入口（S3 TCP + 管理端 Unix Socket）
internal/s3/         S3 协议、SigV4、分块上传、Presign
internal/store/      bbolt 元数据 + 文件对象存储
internal/admin/      管理 API、活动日志、上传/下载/分享
internal/webui/      内嵌管理端 SPA（无构建链、无 CDN）
internal/config/     配置加载与环境变量覆盖
pack/                fnOS fpk 打包目录（manifest、wizard、cmd、图标）
tools/genicon/       图标生成器
build.ps1 / build.sh 交叉编译 + fnpack 打包
```

## 更新日志

### v1.0.20 · 2026-11-17

- 文档示例改用占位值，移除真实的访问密钥、公网地址与桶名
- 修复开发文档中 Lucky 配置截图的红色叉号，改为中性灰删除图标

<details>
<summary>历史版本</summary>

#### v1.0.19 · 2026-11-16

- 统一输入框、下拉框与行内按钮高度（36px），“选择目录”按钮不再比左侧输入框矮一截

#### v1.0.18 · 2026-11-16

- 修复弹窗内表单字段缺乏间距、标签与说明文字挤在一起的问题
- 修复在飞牛目录选择器里点“取消”后误报“仅管理员可进行此操作”

#### v1.0.17 · 2026-11-16

- README 与应用描述明确说明：可直接对接 Typecho 博客，也可按开发文档自行接入博客、图床等应用

#### v1.0.16 · 2026-11-16

- 开发文档「客户端案例」按 Typecho 插件实际实现更新：安装、配置项、三种访问方式与注意事项

#### v1.0.15 · 2026-11-16

- 开发文档新增「客户端案例」：Typecho 博客对接示例
- README 增加客户端案例入口

#### v1.0.14 · 2026-11-16

- 访问密钥改为**单选绑定一个存储桶**，需要访问全部桶时保持“全部桶”
- 密钥范围改用桶名徽标展示，移动端同步适配
- README 新增版本徽标与更新日志

#### v1.0.13 · 2026-11-16

- 移动端汉堡按钮触控区域提升到 36px，抽屉打开时隐藏失效按钮
- 移动端顶栏移除 `backdrop-filter`，修复点击与滚动时的闪烁

#### v1.0.12 · 2026-11-16

- 移动端访问密钥改为卡片布局，不再把表格压成竖排
- 修复长 Access Key / Secret Key 与范围文字溢出
- 移动端弹窗内边距与勾选列表对齐优化

#### v1.0.11 · 2026-11-16

- 访问密钥支持按存储桶授权与只读权限
- `ListBuckets` 与预签名链接按密钥范围过滤
- 管理端支持创建与编辑密钥范围

#### v1.0.10 · 2026-11-16

- 修复移动端卡片撑破视口导致的整页横向溢出

#### v1.0.8 · 2026-11-16

- 顶栏外观按钮支持浅色 / 深色 / 跟随系统三态循环
- 浅色主题侧栏跟随配色，修复移动端用户胶囊

#### v1.0.5 · 2026-11-16

- 界面细节与稳定性修复

#### v1.0.4 · 2026-11-15

- 修复分享链接与长文本横向溢出
- 新增私有桶预签名 URL 网页对接文档
- 修复管理端无尾斜杠访问时页面空白

#### v1.0.1 · 2026-11-15

- 首个公开版本：S3 兼容对象存储、管理端、飞牛打包

</details>
