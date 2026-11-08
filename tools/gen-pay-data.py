"""Convert the client's pay_data_display.lua into data/<region>/server/pay_data.json.

The client's charge goods (the shop's money packs) live only in this Lua table:
the server needs each pack's gems, its catch-up/extra items and its purchase
limits to hand out a free purchase. Only the fields the server reads are kept.
"""

import json
import sys


class Parser:
    def __init__(self, text):
        self.text = text
        self.i = 0

    def skip(self):
        while self.i < len(self.text):
            c = self.text[self.i]
            if c in " \t\r\n,":
                self.i += 1
            elif self.text.startswith("--", self.i):
                j = self.text.find("\n", self.i)
                self.i = len(self.text) if j < 0 else j
            else:
                return

    def string(self):
        # a Lua quoted string, with escapes
        assert self.text[self.i] in "\"'"
        quote = self.text[self.i]
        self.i += 1
        out = []
        while self.i < len(self.text):
            c = self.text[self.i]
            if c == "\\":
                nxt = self.text[self.i + 1]
                out.append({"n": "\n", "t": "\t", "r": "\r"}.get(nxt, nxt))
                self.i += 2
                continue
            if c == quote:
                self.i += 1
                return "".join(out)
            out.append(c)
            self.i += 1
        raise ValueError("unterminated string")

    def value(self):
        self.skip()
        c = self.text[self.i]
        if c == "{":
            return self.table()
        if c in "\"'":
            return self.string()
        if self.text.startswith("true", self.i):
            self.i += 4
            return True
        if self.text.startswith("false", self.i):
            self.i += 5
            return False
        if self.text.startswith("nil", self.i):
            self.i += 3
            return None
        start = self.i
        while self.i < len(self.text) and self.text[self.i] not in ",}\n\r\t ":
            self.i += 1
        raw = self.text[start:self.i]
        try:
            return int(raw)
        except ValueError:
            return float(raw)

    def table(self):
        assert self.text[self.i] == "{"
        self.i += 1
        items = {}
        positional = []
        while True:
            self.skip()
            if self.text[self.i] == "}":
                self.i += 1
                break
            # key = value, or a bare value
            save = self.i
            if self.text[self.i] == "[":
                j = self.text.find("]", self.i)
                key = self.text[self.i + 1:j].strip()
                self.i = j + 1
                self.skip()
                assert self.text[self.i] == "="
                self.i += 1
                items[parse_key(key)] = self.value()
                continue
            # a name followed by "=" is a key
            if self.text[self.i] in "\"'":
                key = self.string()
                self.skip()
                if self.i < len(self.text) and self.text[self.i] == "=":
                    self.i += 1
                    items[key] = self.value()
                    continue
                self.i = save
                positional.append(self.value())
                continue
            ident_start = self.i
            while self.i < len(self.text) and (self.text[self.i].isalnum() or self.text[self.i] == "_"):
                self.i += 1
            ident = self.text[ident_start:self.i]
            self.skip()
            if ident and self.i < len(self.text) and self.text[self.i] == "=":
                self.i += 1
                items[ident] = self.value()
                continue
            self.i = save
            positional.append(self.value())
        if items and positional:
            # mixed: keep the positional ones under their 1-based index
            merged = dict(items)
            for n, value in enumerate(positional, 1):
                merged.setdefault(n, value)
            return merged
        return items if items else positional


def parse_key(key):
    try:
        return int(key)
    except ValueError:
        return key.strip("\"'")


def entries(text):
    marker = "pg.base.pay_data_display["
    pos = 0
    while True:
        start = text.find(marker, pos)
        if start < 0:
            return
        end = text.find("]", start + len(marker))
        row_id = int(text[start + len(marker):end])
        brace = text.find("{", end)
        parser = Parser(text)
        parser.i = brace
        yield row_id, parser.table()
        pos = parser.i


def main():
    region = sys.argv[1]
    source = sys.argv[2]
    target = sys.argv[3]
    text = open(source, "rb").read().decode("utf-8", "replace")
    rows = {}
    for row_id, row in entries(text):
        rows[str(row_id)] = {
            "money": row.get("money", 0),
            "gem": row.get("gem", 0),
            "extra_gem": row.get("extra_gem", 0),
            "extra_service": row.get("extra_service", 0),
            "first_pay_double": row.get("first_pay_double", 0),
            "limit_arg": row.get("limit_arg", 0),
            "limit_type": row.get("limit_type", 0),
            "time": row.get("time", ""),
            "extra_service_item": [
                [int(entry[0]), int(entry[1]), int(entry[2])]
                for entry in row.get("extra_service_item", [])
                if isinstance(entry, list) and len(entry) >= 3
            ],
        }
    with open(target, "w", encoding="utf-8") as out:
        json.dump(rows, out, ensure_ascii=False, separators=(",", ":"))
        out.write("\n")
    print(region, len(rows), "rows ->", target)


if __name__ == "__main__":
    main()
