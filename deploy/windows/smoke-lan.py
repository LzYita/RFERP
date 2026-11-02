#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""RFERP 局域网版验收脚本（单机即可模拟多台设备）。

为什么要有它：真实多机测试需要两三台电脑，而且"第 3 台被拒""撤销后会话立刻失效"
这类判据靠手工点很难稳定复现。设备指纹本来就是客户端自己算的字符串，服务端只
看这个值——所以一个脚本就能把额度、撤销、越权、并发全部跑一遍。

用法（服务端已启动）：

    python smoke-lan.py --phase bare     --base http://127.0.0.1:18080 \
        --lan-base http://192.168.43.252:18080 --password <管理员密码> --token <初始化令牌>

    # 导入授权后
    python smoke-lan.py --phase licensed --base ... --password ...

    # 重启主机后
    python smoke-lan.py --phase restart  --base ... --password ...

退出码 0 表示全部通过；任何 FAIL 都返回 1。脚本只读判据、不改产品配置。
"""

import argparse
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

HOST_DEVICE = None  # 由 phase bare 写入状态文件，供后续阶段复用
STATE_FILE = os.path.join(__import__("tempfile").gettempdir(), "rferp-smoke-lan-state.json")

results = []


def check(name, ok, detail=""):
    results.append((name, ok, detail))
    print(("PASS  " if ok else "FAIL  ") + name + (("  -> " + detail) if detail and not ok else ""))


def call(base, method, path, body=None, token=None, device=None, name=None,
         timeout=15, raw_body=None, extra_headers=None):
    data = raw_body if raw_body is not None else (
        json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(base + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    if device:
        req.add_header("X-RFERP-Device-ID", device)
    if name:
        # 头只能是 latin-1；把 UTF-8 字节原样塞进去，服务端按字节收。
        req.add_header("X-RFERP-Device-Name", name.encode("utf-8").decode("latin-1"))
    for k, v in (extra_headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, dict(r.headers), r.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read()
    except Exception as e:  # 连接失败等
        return 0, {}, str(e).encode()


def jbody(raw):
    try:
        return json.loads(raw)
    except Exception:
        return {}


def dev(ch):
    return ch * 64


def login(base, password, device, name="smoke", user="admin"):
    st, _, raw = call(base, "POST", "/api/login",
                      {"username": user, "password": password}, device=device, name=name)
    return st, jbody(raw).get("token", ""), jbody(raw)


def save_state(d):
    try:
        with open(STATE_FILE, "w", encoding="utf-8") as f:
            json.dump(d, f)
    except OSError:
        pass


def load_state():
    try:
        with open(STATE_FILE, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}


# ---------------------------------------------------------------- bare：空库

def phase_bare(a):
    base, lan = a.base, (a.lan_base or a.base)
    host = a.host_device

    st, _, raw = call(base, "POST", "/api/bootstrap-admin",
                      {"username": "admin", "password": a.password})
    check("空库建管理员：不带令牌被拒(403)", st == 403 and jbody(raw).get("code") == "bootstrap_token_invalid",
          f"status={st} body={raw[:120]!r}")

    body = {"username": "admin", "password": a.password}
    st2, _, _ = call(base, "POST", "/api/bootstrap-admin", body,
                     extra_headers={"X-Bootstrap-Token": "WRONGTOK"})
    check("空库建管理员：错误令牌被拒(403)", st2 == 403, f"status={st2}")

    st3, _, raw3 = call(base, "POST", "/api/bootstrap-admin", body,
                        extra_headers={"X-Bootstrap-Token": a.token})
    check("空库建管理员：正确令牌成功(200)", st3 == 200, f"status={st3} body={raw3[:120]!r}")

    st, _, raw = call(base, "POST", "/api/bootstrap-admin",
                      {"username": "admin2", "password": a.password})
    check("重复初始化被拒(409，而不是 403 令牌错)", st == 409,
          f"status={st} body={raw[:120]!r}")

    st, _, raw = call(base, "POST", "/api/login", {"username": "admin", "password": a.password})
    check("登录缺设备头被拒(400 device_missing)",
          st == 400 and jbody(raw).get("code") == "device_missing", f"status={st}")

    st, _, raw = call(base, "POST", "/api/login", {"username": "admin", "password": a.password},
                      device="abc")
    check("登录设备指纹格式非法被拒(400)", st == 400, f"status={st}")

    # 没有授权：主机放行、客户端拒绝
    st, host_tok, _ = login(base, a.password, host, name="host")
    check("无授权：主机可登录(200)", st == 200, f"status={st}")

    st, _, body = login(base, a.password, dev("1"))
    check("无授权：客户端被拒(403 license_missing)",
          st == 403 and body.get("code") == "license_missing", f"status={st} code={body.get('code')}")

    # 目录脱敏：回环给路径，局域网来源只给 remote
    st, _, raw = call(base, "GET", "/api/datadir", token=host_tok, device=host)
    check("回环访问 /api/datadir 返回 dataDir", st == 200 and b"dataDir" in raw, f"status={st}")

    if lan != base:
        st, _, raw = call(lan, "GET", "/api/datadir", token=host_tok, device=host)
        check("局域网来源 /api/datadir 只回 remote",
              st == 200 and b"remote" in raw and b"dataDir" not in raw, f"status={st} body={raw[:120]!r}")

    # 破坏性操作在服务器形态一律 403
    for path, body in [("/api/restore", {"file": "x.sql"}),
                       ("/api/clear", {"confirm": "drop"}),
                       ("/api/datadir", {"dir": "C:/tmp"})]:
        st, _, raw = call(base, "POST", path, body, token=host_tok, device=host)
        check(f"服务器形态拒绝 {path}(403)", st == 403, f"status={st}")

    # 备份下载路径穿越
    st, _, raw = call(base, "GET", "/api/backup/download?name=..%5Csecret.txt",
                      token=host_tok, device=host)
    check("备份下载拒绝路径穿越(400)", st == 400, f"status={st} body={raw[:120]!r}")

    save_state({"host_device": host})


# ------------------------------------------------------------ licensed：授权后

def phase_licensed(a):
    base = a.base
    host = a.host_device
    run = time.strftime("%H%M%S")

    # 主机 + 客户端 1
    st, admin, body = login(base, a.password, host, name="host")
    check("授权后主机登录(200)", st == 200, f"status={st}")

    # 让脚本可重复执行：清掉上一轮遗留的客户端设备（保留主机）。
    # 不清的话第二次跑会从"额度已满"开始，判据全部错位。
    st, _, raw = call(base, "GET", "/api/devices", token=admin, device=host)
    for d in jbody(raw).get("devices", []):
        if d.get("kind") == "client":
            call(base, "POST", "/api/devices/delete", {"device_id": d["device_id"]},
                 token=admin, device=host)

    st, _, body = login(base, a.password, dev("a"), name="pc-a")
    check("客户端 1 登录(200)", st == 200, f"status={st} code={body.get('code')}")
    tok_a = body.get("token", "")

    # 会话绑定：换设备头必须 401
    st, _, _ = call(base, "GET", "/api/me", token=tok_a, device=dev("b"))
    check("会话绑定：换设备头访问被拒(401)", st == 401, f"status={st}")
    st, _, _ = call(base, "GET", "/api/me", token=tok_a, device=dev("a"))
    check("会话绑定：原设备访问正常(200)", st == 200, f"status={st}")

    # 额度边界（3 客户端授权，已有 1 台）
    st2, tok_b, _ = login(base, a.password, dev("b"), name="pc-b")
    check("客户端 2 登录(200)", st2 == 200, f"status={st2}")
    st3, _, body3 = login(base, a.password, dev("c"), name="pc-c")
    check("客户端 3 登录(200)", st3 == 200, f"status={st3}")
    st4, _, body4 = login(base, a.password, dev("d"), name="pc-d")
    check("第 4 台被额度拒绝(403 device_limit)",
          st4 == 403 and body4.get("code") == "device_limit",
          f"status={st4} code={body4.get('code')}")

    # 已登记设备在超额时仍可登录
    st, _, body = login(base, a.password, dev("a"), name="pc-a")
    check("已登记设备超额后仍可登录(200)", st == 200, f"status={st}")

    # 撤销：会话立即失效 + 再登录被拒
    st, admin, _ = login(base, a.password, host, name="host")  # 重登，避免被踢
    st, _, raw = call(base, "POST", "/api/devices/revoke", {"device_id": dev("a")},
                      token=admin, device=host)
    check("撤销设备(200)", st == 200, f"status={st} body={raw[:120]!r}")
    st, _, _ = call(base, "GET", "/api/me", token=tok_a, device=dev("a"))
    check("撤销后旧会话立即失效(401)", st == 401, f"status={st}")
    st, _, body = login(base, a.password, dev("a"), name="pc-a")
    check("撤销后该设备不能登录(403 device_revoked)",
          st == 403 and body.get("code") == "device_revoked",
          f"status={st} code={body.get('code')}")

    # 撤销不释放名额：新设备仍被拒
    st, _, body = login(base, a.password, dev("e"), name="pc-e")
    check("撤销不释放名额：新设备仍被拒(403 device_limit)",
          st == 403 and body.get("code") == "device_limit",
          f"status={st} code={body.get('code')}")

    # 删除才释放
    st, admin, _ = login(base, a.password, host, name="host")
    st, _, raw = call(base, "POST", "/api/devices/delete", {"device_id": dev("a")},
                      token=admin, device=host)
    check("删除设备(200)", st == 200, f"status={st} body={raw[:120]!r}")
    st, _, body = login(base, a.password, dev("e"), name="pc-e")
    check("删除释放名额：新设备可登录(200)", st == 200, f"status={st} code={body.get('code')}")

    # 空名字不覆盖已有名字
    st, admin, _ = login(base, a.password, host, name="host")
    st, _, raw = call(base, "GET", "/api/devices", token=admin, device=host)
    names = {d["device_id"]: d["name"] for d in jbody(raw).get("devices", [])}
    check("主机名未被空值覆盖", names.get(host) not in (None, "", host[:8]),
          f"host name={names.get(host)!r}")

    # 只读角色：能读不能写
    st, _, raw = call(base, "POST", "/api/users",
                      {"Username": "viewer" + run, "Password": "viewerpass", "Role": "viewer"},
                      token=admin, device=host)
    check("创建只读用户(200)", st == 200, f"status={st} body={raw[:120]!r}")
    # 复用已登记设备：额度按设备数计，新设备会再占一个名额（此时已满）。
    # 这里验证的是角色权限，不是额度，所以用已有设备。
    st, vt, body = login(base, "viewerpass", dev("b"), name="viewer-pc", user="viewer" + run)
    check("只读用户可登录(200)", st == 200, f"status={st} code={body.get('code')}")
    st, _, _ = call(base, "GET", "/api/parts", token=vt, device=dev("b"))
    check("只读用户可读零件(200)", st == 200, f"status={st}")
    st, _, _ = call(base, "POST", "/api/parts/stock-in", {"part_id": 1, "qty": 1},
                    token=vt, device=dev("b"))
    check("只读用户写操作被拒(403)", st == 403, f"status={st}")

    # 并发入库：两台设备各写 10 次，库存必须正好是 20
    st, admin, _ = login(base, a.password, host, name="host")
    st, _, raw = call(base, "POST", "/api/parts",
                      {"code": "SMOKE-" + run, "name": "并发测试零件", "unit": "个",
                       "stock_qty": 0, "warn_qty": 0},
                      token=admin, device=host)
    part = jbody(raw)
    pid = part.get("id", 0)
    check("创建并发测试零件(200)", st == 200 and pid > 0, f"status={st} body={raw[:160]!r}")

    def stock_in(device):
        ok = 0
        for _ in range(10):
            s, _, _ = call(base, "POST", "/api/parts/stock-in",
                           {"part_id": pid, "qty": 1}, token=admin, device=device)
            if s == 200:
                ok += 1
        return ok

    with ThreadPoolExecutor(max_workers=2) as pool:
        done = list(pool.map(stock_in, [host, host]))
    st, _, raw = call(base, "GET", f"/api/parts/get?id={pid}", token=admin, device=host)
    got = jbody(raw).get("stock_qty")
    check("并发入库 20 次，库存正好 20", done == [10, 10] and abs((got or 0) - 20) < 1e-6,
          f"成功次数={done} 库存={got}")

    # 备份：生成 + 列表 + 下载
    st, hdr, raw = call(base, "POST", "/api/backup", {}, token=admin, device=host, timeout=60)
    check("一键备份返回文件流(200 且非空)", st == 200 and len(raw) > 1000,
          f"status={st} bytes={len(raw)}")
    st, _, raw = call(base, "GET", "/api/backup/files", token=admin, device=host)
    files = jbody(raw).get("files", [])
    check("备份列表包含刚生成的备份", len(files) >= 1, f"files={len(files)}")
    if files:
        st, _, raw = call(base, "GET", "/api/backup/download?name=" + files[0]["name"],
                          token=admin, device=host, timeout=60)
        check("下载刚生成的备份(200 且非空)", st == 200 and len(raw) > 1000,
              f"status={st} bytes={len(raw)}")

    # 备份期间并发写入不应 500
    def write_during_backup(_):
        codes = []
        for _ in range(5):
            s, _, _ = call(base, "POST", "/api/parts/stock-in",
                           {"part_id": pid, "qty": 1}, token=admin, device=host)
            codes.append(s)
        return codes

    with ThreadPoolExecutor(max_workers=2) as pool:
        fut = pool.submit(call, base, "POST", "/api/backup", {}, admin, host, None, 60, None)
        codes = list(pool.map(write_during_backup, [1]))
    st_bk = fut.result()[0]
    flat = [c for sub in codes for c in sub]
    check("备份与写入并发：不出现 5xx",
          st_bk == 200 and all(c in (200, 403, 409) for c in flat),
          f"backup={st_bk} writes={flat}")

    # 留给 restart 阶段：一个仍然有效的 token
    save_state({"host_device": host, "token_before_restart": admin})


# --------------------------------------------------------------- restart：重启后

def phase_restart(a):
    base = a.base
    st = load_state()
    host = a.host_device or st.get("host_device")

    old = st.get("token_before_restart", "")
    if old:
        s, _, _ = call(base, "GET", "/api/me", token=old, device=host)
        check("重启后旧会话失效(401)", s == 401, f"status={s}")
    else:
        check("重启后旧会话失效(401)", False, "没有上一阶段的 token")

    st2, admin, body = login(base, a.password, host, name="host")
    check("重启后可重新登录(200)", st2 == 200, f"status={st2}")
    s, _, raw = call(base, "GET", "/api/devices", token=admin, device=host)
    devices = jbody(raw).get("devices", [])
    check("重启后设备登记仍在", len(devices) >= 2, f"devices={len(devices)}")
    rt = jbody(raw).get("runtime", {})
    check("重启后授权仍生效", rt.get("licensed") is True,
          f"runtime={rt}")


# ------------------------------------------------------- expired：试用到期只读

def phase_expired(a):
    base, host = a.base, a.host_device
    st, admin, body = login(base, a.password, host, name="host")
    check("到期后主机仍可登录(200)", st == 200, f"status={st} code={body.get('code')}")

    st, _, raw = call(base, "GET", "/api/license", token=admin, device=host)
    rt = jbody(raw).get("runtime", {})
    check("到期后 runtime.read_only=true", rt.get("read_only") is True, f"runtime={rt}")

    # 业务写入被拒（license_expired），读取不受影响
    st, _, raw = call(base, "POST", "/api/parts",
                      {"code": "EXP-" + time.strftime("%H%M%S"), "name": "到期测试",
                       "unit": "个", "stock_qty": 0, "warn_qty": 0},
                      token=admin, device=host)
    check("到期后业务写入 403 license_expired",
          st == 403 and jbody(raw).get("code") == "license_expired",
          f"status={st} body={raw[:120]!r}")
    st, _, _ = call(base, "GET", "/api/parts", token=admin, device=host)
    check("到期后仍可读取(200)", st == 200, f"status={st}")

    # 管理员/备份类操作必须放行，否则客户没法续期、也没法导数据
    st, _, raw = call(base, "POST", "/api/users",
                      {"Username": "expired" + time.strftime("%H%M%S"),
                       "Password": "viewerpass", "Role": "viewer"},
                      token=admin, device=host)
    check("到期后仍可管理用户(200)", st == 200, f"status={st} body={raw[:120]!r}")
    st, _, raw = call(base, "POST", "/api/backup", {}, token=admin, device=host, timeout=60)
    check("到期后仍可备份(200)", st == 200 and len(raw) > 1000,
          f"status={st} bytes={len(raw)}")


# ------------------------------------------------------ tampered：篡改授权被拒

def phase_tampered(a):
    base, host = a.base, a.host_device
    if not a.license or not os.path.exists(a.license):
        check("篡改授权：需要 --license <合法授权文件>", False, a.license or "未提供")
        return

    st, admin, _ = login(base, a.password, host, name="host")
    st, _, raw = call(base, "GET", "/api/license", token=admin, device=host)
    before = jbody(raw).get("runtime", {}).get("customer")

    with open(a.license, encoding="utf-8") as f:
        env = json.load(f)
    payload = json.loads(base64.b64decode(env["payload"]))
    payload["maxClients"] = 99  # 篡改：改额度但不改签名
    env["payload"] = base64.b64encode(json.dumps(payload).encode()).decode()

    st, _, raw = call(base, "POST", "/api/license/import", {"license": json.dumps(env)},
                      token=admin, device=host)
    check("篡改授权被拒(403 license_invalid)",
          st == 403 and jbody(raw).get("code") == "license_invalid",
          f"status={st} body={raw[:160]!r}")

    st, _, raw = call(base, "GET", "/api/license", token=admin, device=host)
    rt = jbody(raw).get("runtime", {})
    check("导入失败不覆盖原授权", rt.get("customer") == before and rt.get("max_clients") != 99,
          f"customer={rt.get('customer')} max={rt.get('max_clients')}")


# ------------------------------------------------------- version：426 版本门禁

def phase_version(a):
    base, host = a.base, a.host_device

    st, _, raw = call(base, "GET", "/api/v1/serverinfo")
    info = jbody(raw)
    check("serverinfo 下发 minClientVersion", info.get("minClientVersion") == a.min_client,
          f"minClientVersion={info.get('minClientVersion')!r}")

    st, _, raw = call(base, "POST", "/api/login",
                      {"username": "admin", "password": a.password}, device=host,
                      extra_headers={"X-RFERP-Client-Version": "1.0.0"})
    check("旧客户端登录 426 version_incompatible",
          st == 426 and jbody(raw).get("code") == "version_incompatible",
          f"status={st} body={raw[:120]!r}")

    st, tok, _ = login(base, a.password, host, name="host")
    # 正常登录不带版本头 → 也应被拒（无法证明够新）
    st, _, raw = call(base, "POST", "/api/login",
                      {"username": "admin", "password": a.password}, device=host)
    check("不带版本头登录 426", st == 426, f"status={st}")

    st, tok, body = login(base, a.password, host, name="host")
    # login() 不带版本头，所以这里改用显式请求拿会话
    st, _, raw = call(base, "POST", "/api/login",
                      {"username": "admin", "password": a.password}, device=host,
                      extra_headers={"X-RFERP-Client-Version": a.min_client})
    check("达标客户端登录 200", st == 200, f"status={st} body={raw[:120]!r}")
    good = jbody(raw).get("token", "")

    st, _, _ = call(base, "GET", "/api/me", token=good, device=host,
                    extra_headers={"X-RFERP-Client-Version": "1.0.0"})
    check("已认证请求带旧版本 426", st == 426, f"status={st}")
    st, _, _ = call(base, "GET", "/api/me", token=good, device=host,
                    extra_headers={"X-RFERP-Client-Version": a.min_client})
    check("已认证请求带达标版本 200", st == 200, f"status={st}")


# --------------------------------------------------------- jitter：弱网与断连

def write_mode(path, mode):
    with open(path, "w", encoding="utf-8") as f:
        f.write(mode)


def phase_jitter(a):
    if not a.mode_file:
        check("jitter：需要 --mode-file（jitter-proxy 的模式文件）", False)
        return
    base = a.client_base
    if not base:
        check("jitter：需要 --client-base（客户端内嵌服务的地址）", False)
        return

    # 1) 正常链路：登录成功
    write_mode(a.mode_file, "normal")
    time.sleep(0.3)
    st, tok, body = login(base, a.password, a.host_device or dev("a"), name="jitter-pc")
    # 客户端内嵌服务不校验设备头；这里主要看它能否把请求转给主机
    check("弱网测试：正常链路登录成功(200)", st == 200, f"status={st} body={body}")

    # 2) 加 400ms 双向延迟：仍应在超时以内成功
    write_mode(a.mode_file, "delay:400")
    time.sleep(0.3)
    t0 = time.time()
    st, _, _ = call(base, "GET", "/api/parts", token=tok, device=a.host_device or dev("a"), timeout=40)
    dt = time.time() - t0
    check("弱网：400ms 延迟下请求成功且变慢", st == 200 and dt >= 0.5,
          f"status={st} elapsed={dt:.1f}s")

    # 3) 断链：请求必须失败，且不能挂到超时之后还不返回
    write_mode(a.mode_file, "drop")
    time.sleep(0.3)
    t0 = time.time()
    st, _, raw = call(base, "GET", "/api/parts", token=tok, device=a.host_device or dev("a"), timeout=45)
    dt = time.time() - t0
    check("断链：请求失败且有界（<40s）", st != 200 and dt < 40,
          f"status={st} elapsed={dt:.1f}s body={raw[:100]!r}")

    # 4) 恢复链路：同一个会话仍然可用（客户端不需要重启）
    write_mode(a.mode_file, "normal")
    time.sleep(0.5)
    st, _, _ = call(base, "GET", "/api/parts", token=tok, device=a.host_device or dev("a"), timeout=40)
    check("链路恢复后同一会话继续可用(200)", st == 200, f"status={st}")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--phase", required=True,
                   choices=["bare", "licensed", "restart", "expired", "tampered", "version", "jitter"])
    p.add_argument("--base", default="http://127.0.0.1:18080")
    p.add_argument("--lan-base", default="")
    p.add_argument("--password", required=True)
    p.add_argument("--token", default="")
    p.add_argument("--host-device", default="",
                   help="主机设备指纹（服务端启动日志里的那一行；模拟主机登录用）")
    p.add_argument("--license", default="", help="tampered 阶段：合法授权文件路径")
    p.add_argument("--min-client", default="", help="version 阶段：服务端要求的最低版本")
    p.add_argument("--client-base", default="", help="jitter 阶段：客户端内嵌服务地址，如 http://127.0.0.1:18490")
    p.add_argument("--mode-file", default="", help="jitter 阶段：jitter-proxy 的模式文件")
    a = p.parse_args()

    {
        "bare": phase_bare,
        "licensed": phase_licensed,
        "restart": phase_restart,
        "expired": phase_expired,
        "tampered": phase_tampered,
        "version": phase_version,
        "jitter": phase_jitter,
    }[a.phase](a)

    failed = [r for r in results if not r[1]]
    print()
    print(f"共 {len(results)} 项，通过 {len(results) - len(failed)}，失败 {len(failed)}")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
