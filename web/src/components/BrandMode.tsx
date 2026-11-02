import { useEffect, useState } from 'react'

import { lan } from '../lib/lan'

/**
 * 登录页左下角的部署说明。
 *
 * 原来固定写「本机运行 · 数据不离开本机」——在单机版是对的，但客户端模式下
 * 数据其实保存在局域网主机上，这句话会误导用户去本机找备份（FR-16）。
 * 部署形态由 /api/lan/status 回答，拿不到时保留单机文案（最保守、也最常见）。
 */
export function BrandMode() {
  const [text, setText] = useState('本机运行 · 数据不离开本机')

  useEffect(() => {
    let alive = true
    lan
      .status()
      .then((st) => {
        if (!alive) return
        if (st.deployment === 'client') {
          setText('已连接局域网主机 · 数据保存在主机')
        } else if (st.deployment === 'lan') {
          setText('局域网主机 · 数据保存在本机')
        }
      })
      .catch(() => {
        /* 状态端点不可用：保留默认文案 */
      })
    return () => {
      alive = false
    }
  }, [])

  return <p className="mt-1">{text}</p>
}
