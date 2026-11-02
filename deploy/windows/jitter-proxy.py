# -*- coding: utf-8 -*-
"""可控的 TCP 代理：给局域网链路注入延迟 / 断连，用来测客户端的重连与报错。

模式文件内容（每次接受连接时重读，不需要重启代理）：
    normal        正常转发
    delay:400     每个方向额外延迟 400ms（模拟弱网 / 拥塞）
    drop          接受后立刻关闭（模拟目标不可达 / 链路中断）

用法：
    python jitter-proxy.py --listen 127.0.0.1:18380 --upstream 127.0.0.1:18280 \
        --mode-file C:\\Temp\\jitter-mode.txt
"""

import argparse
import socket
import threading
import time


def read_mode(path):
    try:
        with open(path, encoding="utf-8") as f:
            return f.read().strip() or "normal"
    except OSError:
        return "normal"


def parse_delay(mode):
    if mode.startswith("delay:"):
        try:
            return int(mode.split(":", 1)[1])
        except ValueError:
            return 0
    return 0


def pump(src, dst, mode_file, stop):
    """按**每块数据**重读模式。

    不能只在建连时读：HTTP 客户端（Go 的默认 Transport）会复用 keep-alive
    连接，改模式前建立的连接会一直按旧行为跑，注入的延迟/断链根本到不了
    请求上。逐块重读让模式切换对存量连接立即生效。
    """
    try:
        while not stop.is_set():
            data = src.recv(65536)
            if not data:
                break
            mode = read_mode(mode_file)
            if mode == "drop":
                break
            delay_ms = parse_delay(mode)
            if delay_ms:
                time.sleep(delay_ms / 1000.0)
            dst.sendall(data)
    except OSError:
        pass
    finally:
        stop.set()
        for s in (src, dst):
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass


def handle(conn, upstream, mode_file):
    mode = read_mode(mode_file)
    if mode == "drop":
        conn.close()
        return
    try:
        up = socket.create_connection(upstream, timeout=5)
    except OSError:
        conn.close()
        return
    up.settimeout(None)
    conn.settimeout(None)
    stop = threading.Event()
    t = threading.Thread(target=pump, args=(up, conn, mode_file, stop), daemon=True)
    t.start()
    pump(conn, up, mode_file, stop)
    t.join(timeout=1)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--listen", default="127.0.0.1:18380")
    p.add_argument("--upstream", required=True)
    p.add_argument("--mode-file", required=True)
    a = p.parse_args()

    lh, lp = a.listen.rsplit(":", 1)
    uh, up = a.upstream.rsplit(":", 1)
    up = (uh, int(up))

    srv = socket.socket()
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind((lh, int(lp)))
    srv.listen(64)
    print(f"jitter-proxy {a.listen} -> {a.upstream}, mode={read_mode(a.mode_file)}", flush=True)
    try:
        while True:
            conn, _ = srv.accept()
            threading.Thread(target=handle, args=(conn, up, a.mode_file), daemon=True).start()
    except KeyboardInterrupt:
        pass
    finally:
        srv.close()


if __name__ == "__main__":
    main()
