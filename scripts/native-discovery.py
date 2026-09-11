#!/usr/bin/env python3
"""Native acceptance probe. Initializes discovery only; never sends a user turn.

Requires skillverk, Git, Codex and Claude Code on PATH (or --skillverk PATH).
Uses isolated repositories/library. Prints only fixture discovery evidence.
"""
import argparse
import json
import os
import pathlib
import platform
import queue
import shutil
import subprocess
import tempfile
import threading
import uuid


class Protocol:
    def __init__(self, args, cwd, env=None):
        self.process = subprocess.Popen(args, cwd=cwd, env=env, stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                        text=True, encoding="utf-8")
        self.lines = queue.Queue()
        def read():
            for line in self.process.stdout:
                try:
                    self.lines.put(json.loads(line))
                except ValueError:
                    pass
            self.lines.put(None)
        self.reader = threading.Thread(target=read, daemon=True)
        self.reader.start()

    def send(self, value):
        self.process.stdin.write(json.dumps(value) + "\n")
        self.process.stdin.flush()

    def response(self, predicate):
        for _ in range(100):
            msg = self.lines.get(timeout=30)
            if msg is None:
                raise RuntimeError("agent exited before discovery response")
            if predicate(msg):
                return msg
        raise RuntimeError("agent did not return discovery response")

    def close(self):
        self.process.terminate()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)
        self.process.stdin.close()
        self.reader.join(timeout=5)
        self.process.stdout.close()


def codex_skills(repo):
    p = Protocol([shutil.which("codex"), "app-server", "--stdio"], repo)
    try:
        p.send({"id": 0, "method": "initialize", "params": {
            "clientInfo": {"name": "skillverk_acceptance", "version": "1.0"}}})
        init = p.response(lambda m: m.get("id") == 0)
        if "error" in init:
            raise RuntimeError(str(init["error"]))
        p.send({"method": "initialized", "params": {}})
        p.send({"id": 1, "method": "skills/list", "params": {
            "cwds": [str(repo)], "forceReload": True}})
        response = p.response(lambda m: m.get("id") == 1)
        if "error" in response:
            raise RuntimeError(str(response["error"]))
        return response["result"]["data"][0]["skills"]
    finally:
        p.close()


def claude_skills(repo, config):
    env = dict(os.environ, CLAUDE_CONFIG_DIR=str(config))
    # Only the control initialization request is sent, never a user message.
    p = Protocol([shutil.which("claude"), "--print", "--input-format", "stream-json",
                  "--output-format", "stream-json", "--verbose", "--no-session-persistence",
                  "--strict-mcp-config", "--setting-sources", "project", "--tools", "",
                  "--settings", '{"disableAllHooks":true}'], repo, env)
    try:
        p.send({"type": "control_request", "request_id": "skillverk-init", "request": {
            "subtype": "initialize", "hooks": {}, "sdkMcpServers": []}})
        response = p.response(lambda m: m.get("type") == "control_response"
                              and m.get("response", {}).get("request_id") == "skillverk-init")
        inner = response["response"]
        if inner.get("subtype") == "error":
            raise RuntimeError(str(inner))
        return inner["response"].get("commands", [])
    finally:
        p.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--skillverk", default="skillverk")
    args = parser.parse_args()
    binary = shutil.which(args.skillverk) or str(pathlib.Path(args.skillverk).resolve())
    for tool in ("git", "codex", "claude"):
        if not shutil.which(tool):
            raise SystemExit("Missing native acceptance dependency: " + tool)
    if platform.system() == "Windows":
        import ctypes
        import winreg
        if ctypes.windll.shell32.IsUserAnAdmin():
            raise SystemExit("Windows acceptance must run as an ordinary, non-administrator user.")
        try:
            with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,
                                r"SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock") as key:
                developer_mode = winreg.QueryValueEx(key, "AllowDevelopmentWithoutDevLicense")[0]
        except FileNotFoundError:
            developer_mode = 0
        if developer_mode:
            raise SystemExit("Disable Developer Mode for the required Windows acceptance check.")
    report = {"platform": platform.platform(), "agents": {}, "checks": []}
    for tool in ("codex", "claude"):
        report["agents"][tool] = subprocess.check_output([shutil.which(tool), "--version"], text=True).strip()
    with tempfile.TemporaryDirectory(prefix="skillverk-native-") as temp:
        root = pathlib.Path(temp)
        repo = root / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q", str(repo)], check=True)
        source = root / "source"
        source.mkdir()
        config = root / "claude-config"
        config.mkdir()
        name = "skillverk-probe-" + uuid.uuid4().hex[:10]
        def content(description):
            (source / "SKILL.md").write_text("---\nname: " + name + "\ndescription: " + description +
                                             "\n---\nInert discovery fixture. Do not execute any workflow.\n", encoding="utf-8")
        def cli(*words):
            subprocess.run([binary, "--home", str(root / "library"), "--directory", str(repo), *words],
                           check=True, stdout=subprocess.DEVNULL)
        def check(phase, present, description=None):
            for agent, fetch in (("codex", lambda: codex_skills(repo)),
                                 ("claude", lambda: claude_skills(repo, config))):
                rows = [r for r in fetch() if r.get("name") == name]
                assert bool(rows) == present, (phase, agent, rows)
                if description and rows:
                    assert description in rows[0].get("description", ""), (phase, agent, rows)
                report["checks"].append({"phase": phase, "agent": agent, "discovered": bool(rows),
                                         "description": rows[0].get("description") if rows else None})
                print(phase + ": " + agent + " passed", flush=True)
        content("Skillverk acceptance before refresh")
        cli("add", str(source))
        check("import inactive", False)
        cli("on", name)
        check("both active", True, "before refresh")
        content("Skillverk acceptance after refresh")
        cli("update", name)
        check("shared refresh", True, "after refresh")
        cli("off", name)
        check("deactivated", False)
        status = subprocess.check_output(["git", "-C", str(repo), "status", "--porcelain"], text=True)
        assert not status.strip(), status
        report["git_status_clean"] = True
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
