from __future__ import annotations

import base64
import json
import os
import secrets
import shutil
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

from playwright.sync_api import Page, sync_playwright


REPO = Path(__file__).resolve().parents[1]
EXE = REPO / "dist" / "aha2-windows-amd64.exe"
OUTPUT = REPO / "design" / "web-demo" / "screenshots-v1"
PORT = 18767
BASE = f"http://127.0.0.1:{PORT}"


def token() -> str:
    return base64.urlsafe_b64encode(secrets.token_bytes(24)).decode().rstrip("=")


def wait_health() -> None:
    deadline = time.time() + 30
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"{BASE}/healthz", timeout=2) as response:
                if json.load(response).get("ok"):
                    return
        except Exception:
            time.sleep(0.25)
    raise RuntimeError("AHA2 health check timed out")


def register(page: Page, setup: str, password: str) -> None:
    page.goto(BASE, wait_until="networkidle")
    page.locator('[name="setup_token"]').fill(setup)
    page.locator('[name="username"]').fill("owner")
    page.locator('[name="password"]').fill(password)
    page.locator("#auth-form button.primary").click()
    page.wait_for_selector("text=所有项目")


def login(page: Page, password: str) -> None:
    page.goto(BASE, wait_until="networkidle")
    page.locator('[name="username"]').fill("owner")
    page.locator('[name="password"]').fill(password)
    page.locator("#auth-form button.primary").click()
    page.wait_for_selector("text=所有项目")


def create_fixture(page: Page, workspace: Path) -> None:
    page.locator('[data-dialog="project"]').click()
    page.locator('#project-form [name="name"]').fill("AHA2 UI Smoke")
    page.locator('#project-form button.primary').click()
    page.wait_for_selector("text=AHA2 UI Smoke")

    page.locator('[data-dialog="workspace"]').click()
    page.locator('#workspace-form [name="name"]').fill("Local Smoke")
    page.locator('#workspace-form [name="root_path"]').fill(str(workspace))
    page.locator('#workspace-form button.primary').click()
    page.wait_for_selector("text=Local Smoke")

    page.locator('[data-view="settings"]').first.click()
    page.locator("#import-config").click()
    page.wait_for_selector("text=gpt-5.6-sol")

    page.locator('[data-view="tasks"]').first.click()
    page.locator('[data-dialog="task"]').click()
    page.locator('#task-form [name="title"]').fill("Responsive UI validation")
    page.locator('#task-form [name="request"]').fill("Validate the responsive AHA2 task flow.")
    page.locator('#task-form [name="backend"]').select_option("stub")
    page.locator('#task-form [name="model_id"]').select_option("model_stub")
    page.locator('#task-form [name="env_group_id"]').select_option("env_stub")
    page.locator('#task-form button.primary').click()
    try:
        page.wait_for_selector("text=Stub Backend 已完成 Turn 1", timeout=30000)
    except Exception as exc:
        raise AssertionError(f"Task detail did not open. Body: {page.locator('body').inner_text()}") from exc


def assert_no_overflow(page: Page) -> None:
    metrics = page.evaluate(
        """() => ({
          width: document.documentElement.clientWidth,
          scrollWidth: document.documentElement.scrollWidth,
          height: document.documentElement.clientHeight,
          scrollHeight: document.documentElement.scrollHeight
        })"""
    )
    if metrics["scrollWidth"] > metrics["width"] + 1:
        raise AssertionError(f"horizontal overflow: {metrics}")


def main() -> None:
    OUTPUT.mkdir(parents=True, exist_ok=True)
    setup = token()
    password = token()
    data_dir = Path(tempfile.mkdtemp(prefix="aha2-ui-smoke-"))
    workspace = data_dir / "workspace"
    workspace.mkdir()
    env = os.environ.copy()
    env["AHA2_SETUP_TOKEN"] = setup
    env["AHA2_IMPORT_AHA_CONFIG"] = r"E:\AHA\.aha\config.json"
    creation_flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    process = subprocess.Popen(
        [
            str(EXE),
            "serve",
            "--listen",
            f"127.0.0.1:{PORT}",
            "--data-dir",
            str(data_dir),
            "--log-level",
            "warn",
        ],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        creationflags=creation_flags,
    )
    try:
        wait_health()
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            desktop = browser.new_context(viewport={"width": 1536, "height": 1024})
            page = desktop.new_page()
            register(page, setup, password)
            create_fixture(page, workspace)
            assert_no_overflow(page)
            page.screenshot(path=str(OUTPUT / "desktop-task.png"), full_page=False)
            desktop.close()

            mobile = browser.new_context(viewport={"width": 390, "height": 844}, is_mobile=True)
            page = mobile.new_page()
            login(page, password)
            assert_no_overflow(page)
            page.screenshot(path=str(OUTPUT / "mobile-projects.png"), full_page=False)
            page.locator('[data-view="tasks"]').last.click()
            page.locator("[data-task]").first.click()
            page.wait_for_selector("text=Stub Backend 已完成 Turn 1")
            assert_no_overflow(page)
            page.screenshot(path=str(OUTPUT / "mobile-task.png"), full_page=False)
            mobile.close()
            browser.close()
        print(
            json.dumps(
                {
                    "ok": True,
                    "desktop": "1536x1024",
                    "mobile": "390x844",
                    "screenshots": sorted(item.name for item in OUTPUT.glob("*.png")),
                    "temporary_data_cleaned": True,
                },
                ensure_ascii=False,
            )
        )
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
        shutil.rmtree(data_dir, ignore_errors=True)


if __name__ == "__main__":
    main()
