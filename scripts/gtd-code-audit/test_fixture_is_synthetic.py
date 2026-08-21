"""testdata/known_tickets.json 必須是合成資料,不是真實工單。

這支存在是因為:那份 fixture 原本是 16 則**逐字複製的真實內部工單**,而這是一個
公開 repo。裡面有生產 GTD 工單 UUID、微秒時間戳、以及 6 則帶「怎麼利用」的安全
finding —— 其中一則逐字寫著哪 6 種變體實測可以穿過圍欄,而那一行指的碼今天還在。

清乾淨之後原本**沒有任何測試在守它** —— 驗證是人工 `git grep` 做的。人工驗證
不會在下一次有人貼一則真工單進來時變紅,而「貼一則真的進來」正是這個 fixture 最
自然的維護方式(它就是拿真工單當樣本的)。所以這份守門必須是測試不是紀律。

**每一條斷言都釘一個結構性事實,不釘文字品質** —— 「描述寫得夠不夠中性」機械判
不了,硬判就是誤擋。這裡只判「這個東西在不在」。

跑法(A 型零依賴工具,stdlib unittest):
    python3 -m unittest test_fixture_is_synthetic
"""
import json
import os
import re
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURE = os.path.join(HERE, "testdata", "known_tickets.json")


def _items():
    with open(FIXTURE, encoding="utf-8") as fh:
        d = json.load(fh)
    return d if isinstance(d, list) else d.get("tasks", d)


class SyntheticIdentifiers(unittest.TestCase):
    """[F167-01] 工單 id 是凍結的合成集合。

    凍結而不是「檢查格式」:合成 UUID 與生產 UUID 在**形狀上完全一樣**,分不出來。
    唯一機械可判的是「是不是這 16 個我們自己造的」—— 換進任何別的 UUID 就會紅,
    而那正是「有人貼了一則真工單進來」的樣態。
    """

    SYNTHETIC_IDS = frozenset({
    "8655cb61-28d1-408a-bc57-f27ecf766484",
    "f9bd6191-2f5c-4bb7-bbc0-627c084b7c44",
    "cde4dbb2-39a9-43f8-9c26-e9b290e4f446",
    "a58758cf-b472-430f-929d-65d1413c4175",
    "72de9051-8714-483b-af4f-b11e5a1e11cc",
    "57d28d87-20e0-4ce1-a02d-44f5d1330576",
    "e49e6ee5-82bd-4b56-88dd-06cd3a1c2788",
    "59e443c1-f9f3-457d-9c7a-f05562295845",
    "1b5981ef-7f5c-4860-98c3-9b366c34bbbd",
    "f98fa2df-5349-48e8-af65-3abe61ac5895",
    "5656cfde-6f7d-4f16-a601-0b065d30c79e",
    "cd64c30d-afa6-46ec-92af-bcb8efddcf5d",
    "79f8c97b-d7f8-409e-8fbd-f7679b48f64a",
    "61c67e15-d1db-4e64-a492-13d55fd0b405",
    "3482ef30-e30b-42e7-a76b-f2b4dd95048f",
    "8ce55fb9-b9e9-4384-8181-b46d8e1f3133",
    })

    def test_ids_are_exactly_the_frozen_synthetic_set(self):
        got = {t["id"] for t in _items()}
        self.assertEqual(got, self.SYNTHETIC_IDS,
                         "fixture 的 id 集合變了 —— 若是刻意新增樣本,"
                         "MUST 用合成 UUID 並同步更新這個凍結集合")

    def test_count_is_stable(self):
        self.assertEqual(len(_items()), len(self.SYNTHETIC_IDS))


class SyntheticTimestamps(unittest.TestCase):
    """[F167-02] created_at 一律是合成年份。

    真實工單的時間戳是微秒級的生產紀錄,洩漏的是「這個系統什麼時候在動」。
    合成值統一用一個明顯不可能的年份 —— 年份是這裡唯一不需要維護清單就判得出來
    的結構特徵。
    """

    SYNTHETIC_YEAR = "2000"

    def test_every_timestamp_uses_the_synthetic_year(self):
        for t in _items():
            ts = t.get("created_at") or ""
            with self.subTest(ticket=t["id"]):
                self.assertTrue(ts.startswith(self.SYNTHETIC_YEAR),
                                f"{ts} 不是合成年份 —— 真實生產時間戳流進來了")


class NoWeaponisedPayloads(unittest.TestCase):
    """[F167-03] 不得含可直接拿去用的 payload 字元。

    ⚠ **判的是字元不是措辭。** 「這個標記可被偽造」是描述缺陷,該留 —— 那是工單
    的用途。該砍的是「哪幾種變體實測有效」那份清單,以及能直接複製貼上的 literal
    payload。前者機械判不了(描述與教學在文字上分不出來),後者判得了:零寬字元、
    NBSP、全形空白這幾類**在正常的中文工單描述裡不該出現**,出現就是有人把攻擊
    樣本原樣貼了進來。
    """

    WEAPONS = (
        ("零寬字元", re.compile("[​-‏﻿]")),
        ("NBSP", re.compile(" ")),
        ("全形空白", re.compile("　")),
    )

    def test_no_literal_payload_characters(self):
        raw = open(FIXTURE, encoding="utf-8").read()
        for name, pat in self.WEAPONS:
            with self.subTest(weapon=name):
                self.assertEqual(pat.findall(raw), [],
                                 f"fixture 含 {name} —— 那是可直接複製使用的攻擊樣本")

    def test_detector_fires_on_a_known_bad_sample(self):
        """**正對照。** 上一條回綠時要讀得出意思,就得先證明它在壞資料上會叫。"""
        bad = "圍欄標記​可以這樣繞過"
        fired = [n for n, p in self.WEAPONS if p.search(bad)]
        self.assertIn("零寬字元", fired, "偵測器對已知的壞樣本沒叫 —— 它是瞎的")


class TestdataInventory(unittest.TestCase):
    """[F167-04] testdata 目錄裡有什麼要是已知的。

    多一個檔就是多一個沒人審過的資料來源 —— 而這個目錄的內容會被公開。
    """

    KNOWN = {"known_tickets.json"}

    def test_only_known_files_present(self):
        got = {f for f in os.listdir(os.path.join(HERE, "testdata"))
               if not f.startswith(".") and f != "__pycache__"}
        self.assertEqual(got, self.KNOWN,
                         "testdata 多了檔案 —— 新增前先確認它不是真實資料")


if __name__ == "__main__":
    unittest.main()
