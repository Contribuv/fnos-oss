# 私有桶 + 预签名 URL 网页对接文档

适用版本：fnos-oss 1.0.1
本文示例环境：

| 项 | 值 |
| --- | --- |
| 公网入口 | `https://s3.example.com:7755` |
| 存储桶 | `demo-bucket` |
| 对象键示例 | `uploads/avatar/avatar.jpg` |
| Region | `us-east-1` |
| 寻址方式 | path-style（`/桶名/对象键`） |

---

## 1. 结论先行

桶策略设为 `private` 后：

| 请求 | 结果 |
| --- | --- |
| 裸链接 `/demo-bucket/uploads/avatar/avatar.jpg` | `403 AccessDenied` |
| 带有效签名的链接 | `200`，正常显示图片 |
| 签名链接过期 | `403 AccessDenied`（`Request has expired.`） |
| 签名被改动（改域名、端口、路径、参数） | `403 SignatureDoesNotMatch` |
| AccessKey 被停用或删除 | `403 InvalidAccessKeyId` |

所以网页上显示私有图片的唯一方式：**由你的后端用 AccessKey 现场签一个临时 URL，交给 `<img src>`**。签名是纯本地计算，不需要访问 NAS。

---

## 2. 一个完整的签名 URL 由什么组成

```
https://s3.example.com:7755/demo-bucket/uploads/avatar/avatar.jpg
  ?X-Amz-Algorithm=AWS4-HMAC-SHA256
  &X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20261115%2Fus-east-1%2Fs3%2Faws4_request
  &X-Amz-Date=20261115T151804Z
  &X-Amz-Expires=3600
  &X-Amz-SignedHeaders=host
  &X-Amz-Signature=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

| 参数 | 含义 | 注意 |
| --- | --- | --- |
| `X-Amz-Credential` | `AccessKeyID/日期/Region/s3/aws4_request` | 只含 AccessKey ID，不含 Secret |
| `X-Amz-Date` | 签名时间，UTC，格式 `yyyyMMdd'T'HHmmss'Z'` | 与 `Credential` 里的日期必须一致 |
| `X-Amz-Expires` | 有效秒数，`1 ~ 604800`（7 天） | 超过 7 天或 0 会被管理端回落为 3600 |
| `X-Amz-SignedHeaders` | 参与签名的请求头，本项目固定为 `host` | host 含端口 |
| `X-Amz-Signature` | HMAC-SHA256 结果 | 任何一位变化都会 403 |

**签名绑定三样东西**：`host`（含端口）、路径（桶名 + 对象键）、查询参数。其中任何一项变了，签名立即失效。

---

## 3. 前置准备

### 3.1 把桶设为私有

管理端 → 存储桶 → `demo-bucket` → 策略选「私有」，或调用：

```http
PATCH /api/buckets/demo-bucket/policy
Content-Type: application/json

{"policy":"private"}
```

### 3.2 创建访问密钥

管理端 → 访问密钥 → 新建，保存好：

- `AccessKey`：形如 `AKIA...`
- `SecretKey`：只显示一次，务必保存

建议给业务后端单独建一个 Key，命名如 `web-backend`，方便日后单独停用/轮换，不影响其他客户端。

### 3.3 设置公网地址

管理端 → 设置 → 公网地址，填：

```
https://s3.example.com:7755
```

这样管理端生成的分享链接直接可用，不需要手改 host。

### 3.4 确认 Region

管理端 → 设置 → Region，默认 `us-east-1`。业务后端签名时这个值必须一致。

---

## 4. 方式一：管理端生成（只用于临时验证）

管理端 → 文件列表 → 某对象 → 「分享链接」，默认 1 小时。

它内部调用的是：

```http
POST /api/buckets/demo-bucket/presign
Content-Type: application/json

{
  "key": "uploads/avatar/avatar.jpg",
  "method": "GET",
  "expires": 3600,
  "endpoint": "https://s3.example.com:7755"
}
```

响应：

```json
{ "url": "https://s3.example.com:7755/demo-bucket/...?X-Amz-...", "method": "GET", "expires": 3600 }
```

> 这个接口需要飞牛统一网关的管理员登录态（`X-Trim-*`），**网页访客不能直接调用**。它适合你手动验证，不适合生产环境的图片展示。

---

## 5. 方式二：业务后端 SDK 签发（推荐）

签名参数统一为：

| 参数 | 值 |
| --- | --- |
| endpoint | `https://s3.example.com:7755` |
| region | `us-east-1` |
| bucket | `demo-bucket` |
| path style | 必须开启 |
| 有效期 | 建议 300 ~ 900 秒 |

> `endpoint` 必须是**浏览器实际访问的地址**，包含 `:7755`。用内网 IP 签出来的链接拿到外网打开，一定 `SignatureDoesNotMatch`。

### 5.1 Node.js（AWS SDK v3）

```bash
npm i @aws-sdk/client-s3 @aws-sdk/s3-request-presigner
```

```js
import { S3Client, GetObjectCommand } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";

const s3 = new S3Client({
  region: "us-east-1",
  endpoint: "https://s3.example.com:7755",
  forcePathStyle: true,
  credentials: {
    accessKeyId: process.env.OSS_AK,
    secretAccessKey: process.env.OSS_SK,
  },
});

export async function signGet(key, expires = 600) {
  return getSignedUrl(
    s3,
    new GetObjectCommand({ Bucket: "demo-bucket", Key: key }),
    { expiresIn: expires }
  );
}

// Express 路由
app.get("/api/file-url", async (req, res) => {
  const url = await signGet(req.query.key, 600);
  res.json({ url });
});
```

### 5.2 Python（boto3）

```bash
pip install boto3
```

```python
import boto3
from botocore.config import Config

s3 = boto3.client(
    "s3",
    endpoint_url="https://s3.example.com:7755",
    aws_access_key_id=AK,
    aws_secret_access_key=SK,
    region_name="us-east-1",
    config=Config(
        signature_version="s3v4",
        s3={"addressing_style": "path"},
    ),
)

def sign_get(key: str, expires: int = 600) -> str:
    return s3.generate_presigned_url(
        "get_object",
        Params={"Bucket": "demo-bucket", "Key": key},
        ExpiresIn=expires,
    )
```

Flask 示例：

```python
@app.get("/api/file-url")
def file_url():
    return {"url": sign_get(request.args["key"])}
```

### 5.3 Go（AWS SDK v2）

```go
cfg, _ := config.LoadDefaultConfig(ctx,
    config.WithRegion("us-east-1"),
    config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
)

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String("https://s3.example.com:7755")
    o.UsePathStyle = true
})

presigner := s3.NewPresignClient(client)

req, err := presigner.PresignGetObject(ctx,
    &s3.GetObjectInput{
        Bucket: aws.String("demo-bucket"),
        Key:    aws.String("uploads/avatar/avatar.jpg"),
    },
    s3.WithPresignExpires(10*time.Minute),
)
// 返回 req.URL
```

如果你就在本项目里写代码，也可以直接调用 `internal/s3` 的 `Presign`：

```go
link, err := s3.Presign("GET", "https://s3.example.com:7755",
    "demo-bucket", key, "us-east-1", ak, sk, 10*time.Minute)
```

### 5.4 PHP（aws/aws-sdk-php）

```bash
composer require aws/aws-sdk-php
```

```php
$s3 = new Aws\S3\S3Client([
    'version'   => 'latest',
    'region'    => 'us-east-1',
    'endpoint'  => 'https://s3.example.com:7755',
    'use_path_style_endpoint' => true,
    'credentials' => ['key' => AK, 'secret' => SK],
]);

$cmd = $s3->getCommand('GetObject', [
    'Bucket' => 'demo-bucket',
    'Key'    => 'uploads/avatar/avatar.jpg',
]);

$url = (string) $s3->createPresignedRequest($cmd, '+10 minutes')->getUri();
```

### 5.5 其他语言

任何支持 S3 SigV4 预签名的库都可以，只要满足：

1. `endpoint` 指向 `https://s3.example.com:7755`
2. 开启 path-style
3. Region 用 `us-east-1`
4. 只签 `host`（不要额外加入 `content-type` 等头）

---

## 6. 推荐的后端接口设计

### 6.1 单张图片

```http
GET /api/file-url?key=uploads/avatar/avatar.jpg
```

```json
{ "url": "https://s3.example.com:7755/demo-bucket/...?X-Amz-...", "expiresAt": 1763303600 }
```

### 6.2 列表批量签名（推荐）

一次请求返回整页图片，避免每张图都打一次后端：

```http
GET /api/album/13/photos
```

```json
{
  "items": [
    {
      "key": "uploads/avatar/avatar.jpg",
      "url": "https://s3.example.com:7755/demo-bucket/...?X-Amz-...",
      "expiresAt": 1763303600
    }
  ]
}
```

签名是 HMAC 计算，100 张图也只需要几毫秒。

### 6.3 权限校验放在签名之前

```js
app.get("/api/file-url", async (req, res) => {
  if (!req.user) return res.status(401).end();          // 必须登录
  if (!canRead(req.user, req.query.key)) return res.status(403).end();
  res.json({ url: await signGet(req.query.key, 600) });
});
```

**私有桶只保护“没有签名的人”，保护不了“有签名链接的人”。** 用户级权限必须在你的业务后端判断，不能指望 S3。

---

## 7. 前端接入

### 7.1 最简形态

```html
<img id="avatar" alt="avatar" />
```

```js
const { url } = await fetch("/api/file-url?key=uploads/avatar/avatar.jpg")
  .then(r => r.json());
document.querySelector("#avatar").src = url;
```

`<img>` 不需要额外设置，签名参数对浏览器是透明的。

### 7.2 列表 + 懒加载

```js
const { items } = await fetch("/api/album/13/photos").then(r => r.json());

for (const item of items) {
  const img = document.createElement("img");
  img.loading = "lazy";
  img.src = item.url;
  img.decoding = "async";
  img.onerror = async () => {
    // 链接过期或网络问题，重新取一次
    const fresh = await fetch(`/api/file-url?key=${encodeURIComponent(item.key)}`)
      .then(r => r.json());
    img.src = fresh.url;
  };
  list.appendChild(img);
}
```

### 7.3 过期处理策略

- 页面加载时拿到的 URL，在 `expiresAt` 前不要重复请求；
- 有效期建议 300 ~ 900 秒；
- 大图预览、下载按钮在点击时再单独签一次，避免长期持有链接。

### 7.4 前端直接上传（可选）

PUT 预签名也支持：

```js
const { url } = await fetch("/api/upload-url", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ key: "uploads/avatar/new.jpg" }),
}).then(r => r.json());

await fetch(url, { method: "PUT", body: file });
```

前提：

- 后端用 `Presign("PUT", ...)` 或 SDK `PutObjectCommand` 签；
- 桶设置里 CORS 允许你的站点域名与 `PUT` 方法；
- 上传大小受项目限制，超大文件建议走后端流式上传或分段上传。

---

## 8. 安全红线

1. **SecretKey 只在服务端**，不得出现在前端 JS、App 包、小程序里。
2. **不要在前端计算 SigV4**，等于把密钥交给所有访客。
3. **有效期不要超过业务需要**，7 天是上限不是建议值。
4. **数据库存对象键，不存签名 URL**，URL 过期即废。
5. **签名 URL 在有效期内等同公开**，谁拿到都能看；敏感图片用更短的有效期 + 业务侧鉴权。
6. **为业务后端单独建 AccessKey**，泄露时只停用它，不影响其他客户端。
7. **不要把管理端接口 `/api/presign` 暴露给访客**，它需要管理员登录态。

---

## 9. 验收清单

### 9.1 桶已私有

```bash
curl -sI "https://s3.example.com:7755/demo-bucket/uploads/avatar/avatar.jpg" | findstr "HTTP"
# HTTP/1.1 403
```

### 9.2 签名链接可用

用你的后端签一条新链接：

```bash
curl -sI "https://s3.example.com:7755/demo-bucket/uploads/avatar/avatar.jpg?X-Amz-Algorithm=...&X-Amz-Signature=..." | findstr "HTTP"
# HTTP/1.1 200
```

### 9.3 过期后失效

把 `X-Amz-Expires` 改成 `1`，等 2 秒再访问：

```xml
<Error><Code>AccessDenied</Code><Message>Request has expired.</Message></Error>
```

### 9.4 篡改后失效

把 `X-Amz-Signature` 最后一位改掉：

```xml
<Error><Code>SignatureDoesNotMatch</Code></Error>
```

### 9.5 停用 Key 后失效

管理端停用该 AccessKey，再用旧链接访问：

```xml
<Error><Code>InvalidAccessKeyId</Code></Error>
```

---

## 10. 错误码对照表

| Code | HTTP | 含义 | 常见原因 |
| --- | --- | --- | --- |
| `AccessDenied` | 403 | 拒绝访问 | 私有桶裸访问；链接过期 |
| `SignatureDoesNotMatch` | 403 | 签名不匹配 | host 不含端口；手动改了 URL；endpoint 配置错 |
| `InvalidAccessKeyId` | 403 | Key 不存在或已停用 | 删除/停用了 Key；拼写错误 |
| `RequestTimeTooSkewed` | 403 | 时间偏差过大 | 服务器时钟不准；签名的 `X-Amz-Date` 在未来 |
| `NoSuchKey` | 404 | 对象不存在 | 对象被删/改名；key 写错；桶不对 |
| `NoSuchBucket` | 404 | 桶不存在 | 桶名拼错 |
| `AuthorizationHeaderMalformed` | 400 | 认证头格式错误 | 手写 Authorization 而非预签名 |
| `InvalidArgument` | 400 | 参数不合法 | 对象键包含非法段（空段、`.`、`..`） |

---

## 11. 高频问题

**Q1：签名链接 403，报 `SignatureDoesNotMatch`**

按顺序排查：

1. 签名时用的 endpoint 是不是 `https://s3.example.com:7755`（含端口）？
2. 实际访问的 host 是不是同一个？反向代理有没有把 Host 的 `:7755` 丢掉？
3. URL 复制过程中有没有被加空格、被 HTML 转义成 `&amp;`？
4. 对象键里的中文/空格有没有被二次编码？

Lucky 3.x 的具体配置（默认会把 Host 的端口去掉）：

1. 子规则操作模式切到「定制模式」；
2. 确认「使用目标地址 Host 请求头」为关闭；
3. 「高级设置 → 自定义配置」填：

```nginx
proxy_set_header Host $http_host;
```

或在「HTTP 后端请求头」新增 `Host` = `$http_host`。

4. 调试时临时加 `add_header X-Debug-Host $http_host always;`，用 `curl -sI` 确认响应头是 `域名:端口`，确认后删除。保存后重启该 Web 服务规则。

**Q2：链接生成后能用，第二天全 403**

签名 URL 过期了。不要存 URL，存对象键，每次用时现签。

**Q3：签出来的链接在内网能打开，外网 403**

签名时用了 `http://192.168.x.x:9100`。签名绑定 host，改成公网域名后签名就失效了。endpoint 必须写公网地址。

**Q4：浏览器 F12 里图片请求是 403，但地址栏直接粘贴能看**

可能是页面里 URL 被转义（`&` → `&amp;`），或者前端拼接 URL 时对查询参数做了 `encodeURIComponent` 整体编码。整条签名 URL 应该原样使用，不要二次加工。

**Q5：改用私有桶后，之前网页里的图片全部裂了**

正常。旧 URL 没有签名，必须改成“后端下发 URL”的模式。数据库里如果存的是完整 URL，需要迁移成对象键。

**Q6：能不能让链接“一次有效”？**

本项目不支持一次性签名。需要一次性效果的话，在业务后端记录已使用的签名（按 key + 用户），或在链接里加业务 token 做校验。

**Q7：私有桶还需要 Nginx 防盗链吗？**

不需要 Referer 防盗链，但注意：如果你在 Nginx/Lucky 上已经配了 `valid_referers`，它对签名请求同样生效，可能误拦。私有桶场景下建议去掉 Referer 校验，靠签名控制。

**Q8：图片要 CDN 缓存怎么办？**

预签名 URL 每次都不同，CDN 命中率低。两个选择：

1. 在页面会话内复用同一批 URL；
2. 改成业务后端代理读取（URL 固定），响应加 `Cache-Control: private, max-age=86400` 和 `ETag`。

---

## 12. 最小可运行示例（Node.js + 原生前端）

### 后端 `server.js`

```js
import express from "express";
import { S3Client, GetObjectCommand } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";

const s3 = new S3Client({
  region: "us-east-1",
  endpoint: "https://s3.example.com:7755",
  forcePathStyle: true,
  credentials: {
    accessKeyId: process.env.OSS_AK,
    secretAccessKey: process.env.OSS_SK,
  },
});

const app = express();
app.use(express.static("public"));

app.get("/api/file-url", async (req, res) => {
  try {
    const key = String(req.query.key || "");
    if (!key.startsWith("uploads/")) return res.status(400).json({ error: "bad key" });

    const url = await getSignedUrl(
      s3,
      new GetObjectCommand({ Bucket: "demo-bucket", Key: key }),
      { expiresIn: 600 }
    );
    res.json({ url });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

app.listen(3000, () => console.log("http://localhost:3000"));
```

### 前端 `public/index.html`

```html
<!doctype html>
<img id="avatar" alt="avatar" width="240" />
<script>
  fetch("/api/file-url?key=uploads/avatar/avatar.jpg")
    .then(r => r.json())
    .then(({ url }) => { document.querySelector("#avatar").src = url; });
</script>
```

### 运行

```bash
export OSS_AK=AKIA...
export OSS_SK=...
node server.js
```

打开 `http://localhost:3000`，图片能显示即对接成功；再把桶设私有，确认刷新后仍然显示、而去掉签名参数访问返回 403。
