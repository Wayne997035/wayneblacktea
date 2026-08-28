#!/usr/bin/env python3
"""psql 錯誤訊息不得洩漏密碼 —— 跑法:python3 -m unittest test_mask_dsn -v

實際外洩過:psql 逾時,`RuntimeError(f"psql 執行失敗: {e}")` 把
`TimeoutExpired` 的 `str()` 印出來,而它含**整個 argv**,DSN 就在裡面。
輸出進了 stdout → session transcript → 可能被複製進報告與 handoff。
"""
import subprocess
import unittest

from lib.db import mask_dsn

# ⚠ 夾具的假密碼 NEVER 借用真實供應商的憑證前綴(本檔第一版借了 Aiven 服務密碼那個前綴)。
# GitHub Push Protection 掃的是**樣態**,不知道你這串是假的 —— 實測整個 push 被擋下,
# 而唯一的「解法」會是去點 unblock URL,那等於為一個假值訓練自己繞過秘密掃描。
PW = "pw-DUMMY-NOT-A-REAL-SECRET"
DSN = f"postgres://avnadmin:{PW}@pg-x.aivencloud.com:23118/db?sslmode=require"


class TestMaskDsn(unittest.TestCase):
    def test_masks_password_in_plain_dsn(self):
        out = mask_dsn(DSN)
        self.assertNotIn(PW, out)
        self.assertIn("avnadmin:***@", out)

    def test_masks_password_inside_exception_str(self):
        """**這條是本檔存在的理由**:洩漏不是從我們印的字串來的,是從例外物件的
        `str()` 來的 —— 它把整個 argv 塞進去。只遮自己組的訊息擋不到這條路。"""
        e = subprocess.TimeoutExpired(cmd=["psql", DSN, "--csv", "-c", "SELECT 1"], timeout=30)
        out = mask_dsn(e)
        self.assertNotIn(PW, out)

    def test_masks_in_called_process_error(self):
        e = subprocess.CalledProcessError(returncode=2, cmd=["psql", DSN])
        self.assertNotIn(PW, mask_dsn(e))

    def test_leaves_non_credential_text_alone(self):
        """反向:沒有密碼的文字不得被改動。缺這條,把 mask 寫成「一律回固定字串」
        也會讓上面每條變綠,而那會把錯誤訊息本身毀掉。"""
        msg = "psql: connection to server at pg-x.aivencloud.com port 23118 failed"
        self.assertEqual(mask_dsn(msg), msg)

    def test_url_without_password_is_untouched(self):
        """`scheme://host/path`(無憑證)不得被誤改 —— 那會讓一般 URL 在錯誤訊息裡變形。"""
        msg = "see https://github.com/owner/repo/pull/1 for context"
        self.assertEqual(mask_dsn(msg), msg)


if __name__ == "__main__":
    unittest.main()
