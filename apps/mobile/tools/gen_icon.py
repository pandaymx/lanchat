# -*- coding: utf-8 -*-
"""lanchat 移动端 App 图标生成器（纯标准库，无 Pillow）。

设计：蓝紫渐变圆角方块底 + 白色对话气泡（圆角矩形 + 尾巴三角）+ 三个蓝色圆点。
输出 Android mipmap 各密度 ic_launcher.png（RGBA）。
"""
import math
import os
import struct
import zlib


def _png_chunk(tag: bytes, data: bytes) -> bytes:
    return (
        struct.pack(">I", len(data))
        + tag
        + data
        + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
    )


def write_png(path: str, w: int, h: int, rgba: bytes) -> None:
    raw = b"".join(b"\x00" + rgba[y * w * 4 : (y + 1) * w * 4] for y in range(h))
    ihdr = struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0)  # 8-bit RGBA
    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\n")
        f.write(_png_chunk(b"IHDR", ihdr))
        f.write(_png_chunk(b"IDAT", zlib.compress(raw, 9)))
        f.write(_png_chunk(b"IEND", b""))


def lerp(a, b, t):
    return a + (b - a) * t


def make_icon(size: int) -> bytes:
    # 抗锯齿采样（4x4 超采样）
    ss = 4
    S = size * ss
    buf = bytearray(S * S * 4)

    # 渐变角：顶左 #2B6BFF → 底右 #5B8FF9 → 中心 #2B6BFF
    top = (0x2B, 0x6B, 0xFF)
    bottom = (0x5B, 0x8F, 0xF9)

    def rounded_rect(x, y, x0, y0, x1, y1, r):
        if x < x0 + r and y < y0 + r:
            return math.hypot(x - (x0 + r), y - (y0 + r)) <= r
        if x > x1 - r and y < y0 + r:
            return math.hypot(x - (x1 - r), y - (y0 + r)) <= r
        if x < x0 + r and y > y1 - r:
            return math.hypot(x - (x0 + r), y - (y1 - r)) <= r
        if x > x1 - r and y > y1 - r:
            return math.hypot(x - (x1 - r), y - (y1 - r)) <= r
        return x0 <= x <= x1 and y0 <= y <= y1

    def inside(x, y):
        # 白色气泡：主体圆角矩形（60% 宽，44% 高，中心偏上）——先判（在背景内）
        bw, bh = 0.60 * S, 0.44 * S
        bx0, by0 = (S - bw) / 2, 0.16 * S
        # 三个圆点（蓝）
        dot_r = 0.028 * S
        cy = by0 + bh * 0.55
        for k, cx in enumerate((bx0 + bw * 0.32, bx0 + bw * 0.50, bx0 + bw * 0.68)):
            if math.hypot(x - cx, y - cy) <= dot_r:
                return "dot"
        if rounded_rect(x, y, bx0, by0, bx0 + bw, by0 + bh, 0.12 * S):
            return "bubble"
        # 气泡尾巴三角（左下）
        tx0, ty0, tx1 = 0.28 * S, by0 + bh, 0.44 * S
        if tx0 <= x <= tx1 and by0 + bh <= y <= by0 + bh + (x - tx0) * 0.55:
            return "bubble"
        # 背景圆角方块（padding 8%）
        pad = 0.08 * S
        if rounded_rect(x, y, pad, pad, S - pad, S - pad, 0.22 * S):
            return "bg"
        return None

    for j in range(S):
        for i in range(S):
            x = i + 0.5
            y = j + 0.5
            hit = inside(x, y)
            px = i
            py = j
            if hit == "bg":
                t = (x + y) / (2 * S)
                buf[(py * S + px) * 4] = int(lerp(top[0], bottom[0], t))
                buf[(py * S + px) * 4 + 1] = int(lerp(top[1], bottom[1], t))
                buf[(py * S + px) * 4 + 2] = int(lerp(top[2], bottom[2], t))
                buf[(py * S + px) * 4 + 3] = 255
            elif hit == "bubble":
                buf[(py * S + px) * 4] = 255
                buf[(py * S + px) * 4 + 1] = 255
                buf[(py * S + px) * 4 + 2] = 255
                buf[(py * S + px) * 4 + 3] = 255
            elif hit == "dot":
                buf[(py * S + px) * 4] = 0x2B
                buf[(py * S + px) * 4 + 1] = 0x6B
                buf[(py * S + px) * 4 + 2] = 0xFF
                buf[(py * S + px) * 4 + 3] = 255
            else:
                buf[(py * S + px) * 4 + 3] = 0

    # 4x4 超采样降采样
    out = bytearray(size * size * 4)
    for j in range(size):
        for i in range(size):
            acc = [0, 0, 0, 0]
            for dy in range(ss):
                for dx in range(ss):
                    s_idx = ((j * ss + dy) * S + (i * ss + dx)) * 4
                    for c in range(4):
                        acc[c] += buf[s_idx + c]
            base = (j * size + i) * 4
            for c in range(4):
                out[base + c] = acc[c] // (ss * ss)
    return bytes(out)


def main():
    base = "/home/ppmb/code/lanchat/apps/mobile/android/app/src/main/res"
    targets = {
        "mipmap-mdpi": 48,
        "mipmap-hdpi": 72,
        "mipmap-xhdpi": 96,
        "mipmap-xxhdpi": 144,
        "mipmap-xxxhdpi": 192,
    }
    for d, s in targets.items():
        path = os.path.join(base, d, "ic_launcher.png")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        write_png(path, s, s, make_icon(s))
        print("wrote", path, s)
    # 512 源图（round 图标可选）
    write_png(os.path.join(base, "..", "..", "..", "..", "..", "assets", "icon_512.png"), 512, 512, make_icon(512))
    print("done")


if __name__ == "__main__":
    main()
