# 最小联调（本机或服务器 Docker）

## 启动

先准备凭据（`deploy/docker/.env` 不会被提交）：

```bat
cd deploy\docker
copy .env.example .env
rem 编辑 .env，填上自己的强口令
docker compose up -d --build
```

服务：`http://127.0.0.1:8080`（服务器上改为机器 IP）。MySQL 映射 `3306`（联调用）。
未设置 `MYSQL_ROOT_PASSWORD` / `MYSQL_PASSWORD` 时 compose 会直接报错，防止误用示例口令。

## 初始化管理员（空库）

```bat
curl -X POST http://127.0.0.1:8080/api/bootstrap-admin -H "Content-Type: application/json" -d "{\"username\":\"boss\",\"password\":\"pass1234\",\"display_name\":\"Boss\"}"
```

已有用户时返回 409。

## 健康检查与登录

```bat
curl http://127.0.0.1:8080/healthz
curl -X POST http://127.0.0.1:8080/api/login -H "Content-Type: application/json" -d "{\"username\":\"boss\",\"password\":\"pass1234\"}"
```

记下返回的 `token`，后续请求加：`Authorization: Bearer <token>`。

## 桌面客户端

1. 首启选 **连接服务器**，地址填 `http://<主机IP>:8080`
2. 用 bootstrap 创建的账号登录
3. 建零件 → 入库 → 看操作记录

## 清理

```bat
docker compose down
rem 彻底删数据：
docker compose down -v
```

## 安全注意

- **本 API 只提供明文 HTTP，没有 TLS。** 登录口令与会话令牌都会以明文在网络上传输，
  因此**只能在可信内网使用**；一旦跨网段、上公网或走 WiFi，必须先加 TLS（C4：反向代理或内置证书）。
- 口令放在 `deploy/docker/.env`（已 gitignore）；**不要**把真实业务库口令写进仓库。
- `8080` 默认绑定所有网卡；只在本机联调时可改成 `"127.0.0.1:8080:8080"`。
- 不要把本 compose 的 DSN/口令用于真实业务数据。
