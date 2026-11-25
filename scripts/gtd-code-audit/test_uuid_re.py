#!/usr/bin/env python3
"""--ids 的 UUID 驗證必須整串比對 —— 跑法:python3 -m unittest test_uuid_re -v

[F1124-42] 舊 pattern 以 `$` 結尾,`$` 也會比對「最後一個換行之前」,所以
`<uuid>\\n` 通過 `.match`。這條驗證是 id 被拼進 SQL 前唯一的閘,所以測試一律經
`lib.db.fetch_tasks` 的 `ids` 參數進去(真正的閘就是它裡面呼叫 UUID_RE 的那一行),
而不是只打 `db.UUID_RE` 本身。憑證與 subprocess 都被 mock:拒絕類測試必須在驗證
這一步就拋 ValueError,psql 不得被呼叫。
"""
import subprocess
import unittest
from pathlib import Path
from unittest import mock

from lib import db

VALID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
VALID_UPPER = "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA"
ENV_LOCAL = Path("/nonexistent/.env.local")  # _resolve_credentials 已被 patch,不會讀


class TestUuidGate(unittest.TestCase):
    def _fetch(self, ids):
        """經真閘 fetch_tasks 進去;回傳 (例外或 None, subprocess.run 的 mock)。"""
        completed = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="id,status\n", stderr="")
        with mock.patch.object(db, "_resolve_credentials",
                               return_value=("postgres://u:p@h/d", "/dev/null")), \
                mock.patch.object(db.subprocess, "run",
                                  return_value=completed) as run:
            try:
                db.fetch_tasks(ENV_LOCAL, ids=ids)
            except ValueError as e:
                return e, run
        return None, run

    def test_uuid_re_rejects_trailing_newline(self):
        err, run = self._fetch([VALID + "\n"])
        self.assertIsInstance(err, ValueError)
        run.assert_not_called()

    def test_uuid_re_accepts_valid_case_insensitive(self):
        for good in (VALID, VALID_UPPER):
            with self.subTest(good=good):
                err, run = self._fetch([good])
                self.assertIsNone(err)
                run.assert_called_once()

    def test_uuid_re_rejects_injection_and_length(self):
        bad_inputs = [
            VALID + "';DROP TABLE tasks;--",  # 合法 UUID 開頭 + 注入字串
            VALID + "a",                      # 合法 UUID 開頭 + 單一多餘字元
            VALID[:-1],                       # 35 字元
            "x" + VALID,                      # 前綴多餘
            "",                               # 空字串
            "not-a-uuid",
        ]
        for bad in bad_inputs:
            with self.subTest(bad=bad):
                err, run = self._fetch([bad])
                self.assertIsInstance(err, ValueError)
                run.assert_not_called()

    def test_uuid_re_baseline_old_pattern_accepts_trailing_newline(self):
        """正對照:改前的 pattern(含 `$`)用 `.match` 對尾端換行回真 ——
        證明上面的拒絕測試看得到這個缺陷;`.fullmatch` 同一個 pattern 擋得住。"""
        import re
        old = re.compile(
            r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$')
        self.assertIsNotNone(old.match(VALID + "\n"))
        self.assertIsNone(old.fullmatch(VALID + "\n"))


if __name__ == "__main__":
    unittest.main()
