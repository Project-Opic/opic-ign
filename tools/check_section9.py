#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""第九部分「UI 界面说明」子条 1 的 13 项自检。

判据出处：《UI-UE 界面设计规范》16.5 自检清单（13 项）+ 16.1/16.2/7.1，
以及《软件需求编写规范 V10.0》第十三章自检项 51~52。

用法：
    python3 tools/check_section9.py design/spec/需求规格说明书-OPIC-<域>.md \
        --html design/spec/需求规格说明书-OPIC-<域>.html

退出码：0 = 13 项全过；1 = 存在 FAIL。WARN 不影响退出码，但须如实写入《编制说明》。

各项判据的口径说明（避免"脚本判据与规范判据各说各话"）：
  · 第 6 项「设计意图」按规范的**结构启发式**判定（分号分段 + 末段字数），不用关键词表；
  · 第 8 项「载体存在性」**逐域判断**，绝不做全篇等式（列表维护形态一域内就有 2 个载体）；
  · 第 10 项表单字段（`field-label`）不可回溯即 FAIL；列表表头（`th`）判 WARN —— 原型骨架
    里的「操作」「实例化时的填法」这类展示列不属字段级权威，规范 16.4 也只约束字段；
  · 第 11 项类名不在色号集合内 FAIL；文案未出现在第七部分判 WARN —— 原型里 badge 兼作
    规则注记（如「被 2 个集群引用，拒删」），不是状态标签；
  · 第 13 项需要 `--html`，未提供时该项记 SKIPPED 并整体判 FAIL（转换后校验不可省略）。
    本项判"转换层"三件事：转义残留 = 0、h2/h3/h4 中文锚点 = 0、` ```mermaid ` 围栏数与
    HTML 内 `class="mermaid"` 载体数相等。**图是否真的渲染成 SVG 不在本脚本判定范围**
    —— 静态 HTML 里本就没有渲染结果，该判据要用无头浏览器取渲染后 DOM，实测数字记 E.4。
"""

import argparse
import json
import os
import re
import sys

FORMS = ("单表维护形态", "列表维护形态", "主从表维护形态", "查询列表形态", "审核界面形态")
BADGE_COLORS = ("b-succ", "b-warn", "b-danger", "b-info", "b-neutral")
SUB_ITEMS = ("UI 界面说明", "界面元素表", "依赖对象与 CUD 操作", "授权角色",
             "流程说明", "界面初始化说明", "界面事件联动规则")
CODE_RE = re.compile(r"\b(?:FUNC|B-APP|B-AUTO|Q)-\d+\b")
BIZ_CODE_RE = re.compile(r"\b([A-Z]{3})-(\d{8})-(\d{4})\b")


def read(path):
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def split_domains(text):
    """按 `^## 9.N ` 切分定义域；9.0 是交付说明导语，不是定义域。"""
    marks = [(m.start(), int(m.group(1)), m.group(2).strip())
             for m in re.finditer(r"(?m)^## 9\.(\d+) (.+)$", text)]
    domains = []
    for i, (pos, num, title) in enumerate(marks):
        if num == 0:
            continue
        end = marks[i + 1][0] if i + 1 < len(marks) else len(text)
        domains.append({"num": num, "title": title, "body": text[pos:end]})
    return domains


def sub_item(domain, idx):
    pat = re.compile(r"(?m)^### 9\.%d\.%d .*$" % (domain["num"], idx))
    m = pat.search(domain["body"])
    if not m:
        return None
    nxt = re.compile(r"(?m)^#{2,3} ").search(domain["body"], m.end())
    return domain["body"][m.end():nxt.start()] if nxt else domain["body"][m.end():]


def html_blocks(section):
    return re.findall(r"(?ms)^```([a-zA-Z]*)\n(.*?)^```", section)


def guide_sentence(section):
    """首个 ``` 围栏之前的第一段非空文本（含续行）。"""
    before = section.split("```", 1)[0]
    lines = [ln.strip() for ln in before.splitlines() if ln.strip()]
    lines = [ln for ln in lines if not ln.startswith("#")]
    return " ".join(lines)


class Check:
    def __init__(self, num, name):
        self.num = num
        self.name = name
        self.fail = []
        self.warn = []
        self.measured = {}

    def add(self, kind, msg):
        (self.fail if kind == "FAIL" else self.warn).append(msg)

    @property
    def status(self):
        if self.fail:
            return "FAIL"
        return "PASS" if self.measured.get("ran", True) else "SKIPPED"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("md")
    ap.add_argument("--html")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    text = read(args.md)
    domains = split_domains(text)
    part9_start = re.search(r"(?m)^## 9\.0 ", text)
    part7 = text[:part9_start.start()] if part9_start else text
    part9 = text[part9_start.start():] if part9_start else ""

    checks = [Check(i, n) for i, n in [
        (1, "子条标题形式"), (2, "引导句存在"), (3, "形态标识合法"), (4, "无计数式描述"),
        (5, "有关键动作"), (6, "有设计意图"), (7, "载体正确"), (8, "载体存在性"),
        (9, "图注存在"), (10, "字段可回溯"), (11, "状态标签合规"), (12, "编号示例合规"),
        (13, "转换后无异常"),
    ]]
    ck = {c.num: c for c in checks}

    # 0 项前置：定义域与 7 子条齐备（规范 13.2 / 自检项 51）
    structure_fail = []
    for d in domains:
        for i in range(1, 8):
            if sub_item(d, i) is None:
                structure_fail.append("9.%d 缺子条 %d（%s）" % (d["num"], i, SUB_ITEMS[i - 1]))
    if len(domains) < 1:
        structure_fail.append("未切出任何定义域")

    fig_nums = []
    carriers_per_domain = {}
    modal_titles = {}
    for d in domains:
        s1 = sub_item(d, 1) or ""
        tag = "9.%d" % d["num"]
        if not re.search(r"(?m)^### 9\.%d\.1 UI 界面说明$" % d["num"], d["body"]):
            ck[1].add("FAIL", "%s 子条 1 标题不是 `### 9.x.1 UI 界面说明`" % tag)
        if re.search(r"(?m)^####|^### （\d+）", d["body"]):
            ck[1].add("FAIL", "%s 出现四级标题或括号式子条标题" % tag)

        guide = guide_sentence(s1)
        if not guide:
            ck[2].add("FAIL", "%s 无引导句" % tag)
        elif not guide.startswith("界面原型（"):
            ck[2].add("FAIL", "%s 引导句未以「界面原型（」开头：%s" % (tag, guide[:30]))
        else:
            ck[2].name = ck[2].name  # keep
        if guide:
            m = re.match(r"界面原型（([^）]*)）", guide)
            if not m:
                ck[3].add("FAIL", "%s 形态标识括号缺失" % tag)
            else:
                raw = m.group(1)
                base = re.split(r"[·+]", raw)[0].strip()
                if base not in FORMS:
                    ck[3].add("FAIL", "%s 形态标识「%s」不在 5 类枚举" % (tag, base))
                else:
                    ck[3].measured.setdefault("forms", []).append(raw)
            # 判据口径：规范禁的是「工具栏（7 项查询条件）+ 列表（10 列）」这类**阿拉伯数字控件计数**
            # （字段增删即失真）。汉字数词指代前文已命名的列（如「这两列永不合并」）是规则表述，
            # 不是控件计数，不判 FAIL —— 否则会把域内硬规则误杀。
            g2 = re.sub(r"第\s*\d+\s*项", "", guide)  # 「8.6 第 18 项」是文档条款互引，不是控件计数
            if re.search(r"[（(]\s*\d+\s*[项列]", g2) or re.search(r"\b\d+\s*[项列]\b", g2) \
                    or re.search(r"\d+\s*(?:项|列)\s*(?:查询|条件|字段|按钮|操作)", g2):
                ck[4].add("FAIL", "%s 引导句含计数式描述：%s"
                          % (tag, re.findall(r".{0,8}\d+\s*[项列]", g2)))
            if not CODE_RE.search(guide):
                ck[5].add("FAIL", "%s 引导句无带编号的关键动作" % tag)
            segs = [s for s in re.split("；", guide) if s.strip()]
            if len(segs) < 2 or len(segs[-1].strip()) < 12:
                ck[6].add("FAIL", "%s 引导句未按「；」分出意图段或末段不足 12 字" % tag)

        blocks = html_blocks(s1)
        # 载体 = 原型容器：`.mock` 外壳与 `.mock-modal` 弹窗各计一个（规范 16.5 第 8 项
        # 明示"同一域内 2 个载体"）。注意只取**精确** `class="mock-modal"` ——
        # `mock-modal-title` 是弹窗内的标题行，不是第二个载体，按子串匹配会虚增。
        n_mock = len(re.findall(r'class="mock"', s1))
        n_modal = len(re.findall(r'class="mock-modal"', s1))
        carriers_per_domain[d["num"]] = n_mock + n_modal
        modal_titles[d["num"]] = len(re.findall(r'class="mock-modal-title"', s1))
        if n_mock + n_modal < 1:
            ck[8].add("FAIL", "%s 载体数 0（class=\"mock\" + class=\"mock-modal\"）" % tag)
        for lang, block in blocks:
            if lang != "html":
                ck[7].add("FAIL", "%s 原型围栏 lang=%r（应为 html）" % (tag, lang))
            for bad in ("<style", "<script", "src=", "href=", "http://", "https://"):
                if bad in block:
                    ck[7].add("FAIL", "%s 原型块含禁止内容 %s" % (tag, bad))
        for other in re.findall(r"(?ms)^```(mermaid)\n", s1):
            ck[7].add("FAIL", "%s 子条 1 内出现 %s 围栏" % (tag, other))

        for cap in re.finditer(r'<p class="fig-cap">图 9-(\d+)', s1):
            fig_nums.append(int(cap.group(1)))
        if len(blocks) != len(re.findall(r'<p class="fig-cap">图 9-', s1)):
            ck[9].add("FAIL", "%s 原型块 %d 个 / 图注 %d 条不匹配"
                      % (tag, len(blocks), len(re.findall(r'<p class="fig-cap">图 9-', s1))))

        s2 = sub_item(d, 2) or ""
        s2_rows = [ln.split("|")[1].strip() for ln in s2.splitlines()
                   if ln.startswith("|") and not re.match(r"^\|\s*[-:| ]+\|$", ln)]
        # 子条 2 常把同族列并写一行（"集群编号 / 名称 / 节点数"），按分隔符拆成 token 再比
        s2_tokens = set()
        for r in s2_rows:
            for tok in re.split(r"[/、,，｜|]|\s{2,}", r):
                tok = re.sub(r"[（(].*?[)）]", "", tok).strip()
                if len(tok) >= 2:
                    s2_tokens.add(tok)

        def covered(name):
            core = re.sub(r"[（(].*?[)）]", "", name).strip()
            if not core:
                return True
            if any(core in r or r.split("（")[0] in core for r in s2_rows if r):
                return True
            return any(tok in core or core in tok for tok in s2_tokens)

        exempt = "字段同上" in s2 or "见子条 2" in s2
        for lbl in re.findall(r'<label class="field-label"[^>]*>([^<]+)</label>', s1):
            if not exempt and not covered(lbl):
                ck[10].add("FAIL", "%s 表单字段「%s」在子条 2 无同名行" % (tag, lbl))
        for th in sorted(set(re.findall(r"<th>([^<]+)</th>", s1))):
            if not covered(th):
                ck[10].add("WARN", "%s 列表列「%s」未见于子条 2（展示列，不判 FAIL）" % (tag, th))

        for cls, body_ in re.findall(r'<span class="badge ([^"]*)">([^<]*)</span>', s1):
            if not any(c in cls.split() for c in BADGE_COLORS):
                ck[11].add("FAIL", "%s badge 色号「%s」不在集合" % (tag, cls))
            elif body_ and body_ not in part7 and "—" not in body_:
                ck[11].add("WARN", "%s badge 文案「%s」未出现在第七部分（规则注记用法）" % (tag, body_))
        for cls in re.findall(r'class="[^"]*\b(b-[a-z]+)\b[^"]*"', s1):
            if cls not in BADGE_COLORS:
                ck[11].add("FAIL", "%s 未定义色号 %s" % (tag, cls))

        for pfx, d8, s4 in BIZ_CODE_RE.findall(s1):
            if not re.match(r"20\d{6}$", d8):
                ck[12].add("FAIL", "%s 编号示例日期异常：%s-%s-%s" % (tag, pfx, d8, s4))
            if pfx not in part7:
                ck[12].add("FAIL", "%s 编号前缀 %s 未在第七部分/3.5 声明" % (tag, pfx))

    ck[1].measured["domains"] = len(domains)
    ck[1].measured["structure"] = "7 子条齐备" if not structure_fail else structure_fail
    for f in structure_fail:
        ck[1].add("FAIL", f)
    ck[3].measured["carriers_total"] = sum(carriers_per_domain.values())
    ck[3].measured["mock_shells"] = sum(len(re.findall(r'class="mock"', sub_item(d, 1) or "")) for d in domains)
    ck[3].measured["mock_modals"] = sum(len(re.findall(r'class="mock-modal"', sub_item(d, 1) or "")) for d in domains)
    ck[3].measured["mock_modal_titles"] = sum(modal_titles.values())
    ck[8].measured["per_domain"] = carriers_per_domain
    ck[9].measured["fig_seq"] = fig_nums
    exp = list(range(1, len(fig_nums) + 1))
    if sorted(fig_nums) != exp:
        ck[9].add("FAIL", "图 9-x 序号不连续或重号：%s" % sorted(fig_nums))
    ck[11].measured["color_vocab"] = list(BADGE_COLORS)

    # 13 转换后
    if args.html:
        h = read(args.html)
        residue = h.count("&lt;div class=&quot;mock")
        cn_anchor = [m.group(0) for m in re.finditer(r'(?m)<h[234][^>]*id="[^"]*[^\x00-\x7F][^"]*"', h)]
        ck[13].measured.update({"escaped_residue": residue, "chinese_anchor_h234": len(cn_anchor),
                               "mermaid_blocks": len(re.findall(r'class="mermaid"', h))})
        if residue:
            ck[13].add("FAIL", "转义残留 %d 处" % residue)
        if cn_anchor:
            ck[13].add("FAIL", "中文锚点 %d 处（示例 %s）" % (len(cn_anchor), cn_anchor[0][:60]))
        caps = len(re.findall(r"(?m)^```mermaid\b", text))
        blocks = len(re.findall(r'class="mermaid"', h))
        ck[13].measured["mermaid_blocks_md_vs_html"] = "%d / %d" % (caps, blocks)
        if caps != blocks:
            ck[13].add("FAIL", "Mermaid 围栏 md=%d 与 HTML 载体块=%d 不一致" % (caps, blocks))
        # 渲染成功与否属浏览器 DOM 层判据（源码里没有 SVG），本脚本不冒充该项；
        # 实测用无头浏览器取 `svg[aria-roledescription]` 计数，记录写《编制说明》E.4。
    else:
        ck[13].measured["ran"] = False
        ck[13].add("FAIL", "未提供 --html，转换后校验不可省略")

    failed = [c for c in checks if c.status == "FAIL"]
    if args.json:
        print(json.dumps([{
            "num": c.num, "name": c.name, "status": c.status,
            "measured": c.measured, "fail": c.fail[:12], "warn": c.warn[:12],
        } for c in checks], ensure_ascii=False, indent=1))
    else:
        print("== 第九部分子条 1 的 13 项自检：%s ==" % os.path.basename(args.md))
        print("定义域 %d 个 / 载体 %d 个（外壳 %d + 弹窗 %d，弹窗标题行 %d 不计载体）/ 图 9-x 序号 %s"
              % (len(domains), ck[3].measured["carriers_total"], ck[3].measured["mock_shells"],
                 ck[3].measured["mock_modals"], ck[3].measured["mock_modal_titles"],
                 ck[9].measured["fig_seq"]))
        for c in checks:
            line = "[%2d] %-12s %s" % (c.num, c.name, c.status)
            extra = {k: v for k, v in c.measured.items() if k not in ("forms", "per_domain", "fig_seq")}
            if extra:
                line += "  " + json.dumps(extra, ensure_ascii=False)
            print(line)
            for m in c.fail[:8]:
                print("       FAIL %s" % m)
            for m in c.warn[:5]:
                print("       warn %s" % m)
        print("=> %d / 13 通过%s" % (13 - len(failed), "" if not failed else "（FAIL 项见上）"))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
