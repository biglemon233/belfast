"""验证官方表的编码、去重与字段变化处理。

Verifies source encodings, deduplication and rejection of changed columns.
"""

import unittest
from build_device_catalog import build_catalog


class DeviceCatalogTest(unittest.TestCase):
    """覆盖会影响离线匹配的数据变化。 / Covers data changes affecting offline lookup."""

    def test_utf16_and_deduplication(self):
        source = ("Retail Branding,Marketing Name,Device,Model\r\n"
                  "Xiaomi,红米 Note 10 Pro,chopin,M2104K10AC\r\n")
        source += "Xiaomi,红米 Note 10 Pro,chopin,M2104K10AC\r\n"
        self.assertEqual(build_catalog(source.encode("utf-16")),
                         "xiaomi\tm2104k10ac\tchopin\t红米 Note 10 Pro\n".encode())

    def test_utf8_and_empty_rows(self):
        source = ("Retail Branding,Marketing Name,Device,Model\n"
                  "Google,Pixel 9,tokay,Pixel 9\n"
                  ",Unknown,,\n")
        self.assertEqual(build_catalog(source.encode("utf-8-sig")),
                         b"google\tpixel 9\ttokay\tPixel 9\n")

    def test_changed_columns_and_empty_catalog(self):
        for source in (b"brand,model\nGoogle,Pixel\n",
                       b"Retail Branding,Marketing Name,Device,Model\n"):
            with self.assertRaises(ValueError):
                build_catalog(source)


if __name__ == "__main__":
    unittest.main()
