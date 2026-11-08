"""Generate data/<region>/server/equipment_boxes.json from a gift box's text.

Some gift boxes have no display_icon bucket in the client data: their
description lists the gear, one "+6 <name>T<n>*<count>" per line. This turns
69001 装备补给箱T3 (the ¥30 starter pack's box, pay 2009) into the equipment
list the server grants.

Usage: python tools/gen-equipment-boxes.py
"""

import json
import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REGION = os.path.join(ROOT, "data", "CN")
VIRT = os.path.join(REGION, "sharecfgdata", "item_virtual_data_statistics.json")
EQUIPS = os.path.join(REGION, "sharecfgdata", "equip_data_statistics.json")
NAMES = os.path.join(REGION, "ShareCfg", "name_code.json")
OUT = os.path.join(REGION, "server", "equipment_boxes.json")

virt = json.load(open(VIRT, encoding="utf-8"))
equips = json.load(open(EQUIPS, encoding="utf-8"))
name_codes = json.load(open(NAMES, encoding="utf-8"))

by_name = {}
for key, row in equips.items():
    by_name.setdefault(row.get("name", ""), []).append(int(key))
for ids in by_name.values():
    ids.sort()

line_re = re.compile(r"^\+(\d+)\s+(.*?)(?:T(\d))?\*(\d+)$")
code_re = re.compile(r"\{namecode:(\d+)\}")


def resolve(name, tier):
    name = code_re.sub(lambda m: name_codes[m.group(1)]["name"], name)
    candidates = by_name.get(name)
    if candidates is None:
        candidates = sorted(k for n, ids in by_name.items() if n.startswith(name) for k in ids)
    if not candidates:
        return None
    if tier is None:
        return candidates[0]
    index = tier - 1
    if index >= len(candidates):
        return None
    return candidates[index]


contents = []
for line in virt["69001"]["display"].split("\n"):
    line = line.strip()
    if not line or not line.startswith("+"):
        continue
    match = line_re.match(line)
    if not match:
        continue
    _plus, name, tier, count = match.groups()
    equip_id = resolve(name, int(tier) if tier else None)
    if equip_id is None:
        print("unresolved", line)
        continue
    contents.append([equip_id, int(count)])

os.makedirs(os.path.dirname(OUT), exist_ok=True)
with open(OUT, "w", encoding="utf-8") as f:
    json.dump({"69001": contents}, f, ensure_ascii=False, indent=1)
print(len(contents), "entries ->", OUT)
