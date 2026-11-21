#!/usr/bin/env python3
"""Inventory registered HTTP operations and literal Web call sites.

No runtime database, credentials or network are used. The checked-in ledger is
deliberately explicit about unfinished operations; --complete is the release
gate and rejects every pending row. Registration syntax outside the supported
forms fails closed rather than silently disappearing from the inventory.
"""
import argparse
import csv
import io
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LEDGER = ROOT / "docs/pet-mcp/coverage.tsv"
WEB = ROOT / "docs/pet-mcp/web-call-sites.tsv"


def routes():
    rows = {}
    constants = {}
    for source_path in (ROOT / "internal/httpapi").glob("*.go"):
        if not source_path.name.endswith("_test.go"):
            constants.update(re.findall(r'\b(\w+)\s*=\s*"([^"\n]+)"', source_path.read_text()))
    for path in sorted((ROOT / "internal/httpapi").glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        source = path.read_text()
        for match in re.finditer(r'mux\.Handle(?:Func)?\(([^\n]+)', source):
            line = match.group(1)
            symbol = re.match(r'(\w+)\s*,',line)
            if symbol and symbol.group(1) in constants:
                line = '"' + constants[symbol.group(1)] + '",' + line[symbol.end():]
            literal = re.match(r'"([^"\n]+)"\s*,', line)
            if literal:
                pattern = literal.group(1)
                names = re.findall(r'\bs\.([A-Za-z0-9_]+)', line)
                handler = names[-1] if names else "inline"
                boundary = "agent_turn" if "withAgentCapability" in line else "owner_session" if "withAuth" in line else "transport_auth"
                add_route(rows, pattern, path, handler, boundary)
            elif path.name == "desktop_handlers.go" and line.startswith("route.pattern,"):
                # Both finite desktop tables are expanded below. Any third
                # dynamic registration must be taught to this scanner.
                continue
            else:
                raise ValueError(f"unsupported registration: {path.name}: {line}")
        if path.name == "desktop_handlers.go":
            if len(re.findall(r'mux\.Handle\(route.pattern,', source)) != 2:
                raise ValueError("desktop route tables changed; review scanner")
            for expression, handler in re.findall(r'\{([^\n]+?),\s*s\.(\w+)\},', source):
                if not re.match(r'"(?:GET|POST|DELETE|PUT|PATCH) ', expression):
                    continue
                expression = expression.replace('prefix', '"/api/v1/tasks/{id}/desktop"')
                parts = re.findall(r'"([^"\n]*)"', expression)
                remainder = re.sub(r'"[^"\n]*"|[+\s]', '', expression)
                if remainder:
                    raise ValueError(f"unsupported desktop pattern: {expression}")
                pattern = ''.join(parts)
                add_route(rows, pattern, path, handler, "agent_turn" if "/agent/" in pattern else "owner_session")
    return rows


def add_route(rows, pattern, path, handler, boundary):
    # Static resources and methodless fallback are still accounted for.
    method, sep, route = pattern.partition(' ')
    if not sep:
        method, route = "ANY", pattern
    key = f"{method} {route}"
    if key in rows:
        raise ValueError(f"duplicate route: {key}")
    rows[key] = {"operation": key, "source": path.name, "handler": handler, "boundary": boundary}


def js_literal_end(source, start):
    quote = source[start]
    index = start + 1
    while index < len(source):
        char = source[index]
        if char == '\\':
            index += 2
            continue
        if char == quote:
            return index + 1
        if quote == '`' and source.startswith('${', index):
            index += 2
            depth = 1
            while index < len(source) and depth:
                char = source[index]
                if char in "\"'`":
                    index = js_literal_end(source, index)
                    continue
                depth += (char == '{') - (char == '}')
                index += 1
            if depth:
                raise ValueError('unterminated Web template expression')
            continue
        index += 1
    raise ValueError('unterminated Web literal')


def web_sites():
    result = []
    for path in sorted((ROOT / "web/src").rglob("*.ts")):
        source = path.read_text()
        # Includes API.request/cache wrappers, raw fetch, downloads and streams.
        for match in re.finditer(r'["\x27`]\/api\/', source):
            end = js_literal_end(source, match.start())
            value = source[match.start() + 1:end - 1]
            tail = source[end:source.find(';', end) + 1]
            method = re.search(r'method:\s*["\x27](GET|POST|PUT|PATCH|DELETE)["\x27]', tail)
            result.append({"source": str(path.relative_to(ROOT)), "line": source.count('\n', 0, match.start()) + 1,
                           "method_hint": method.group(1) if method else "GET/see-source", "expression": value})
    return result


def encode(fields, rows):
    output = io.StringIO()
    writer = csv.DictWriter(output, fields, delimiter='\t', lineterminator='\n')
    writer.writeheader()
    writer.writerows(rows)
    return output.getvalue()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--update', action='store_true')
    parser.add_argument('--complete', action='store_true')
    args = parser.parse_args()
    actual = routes()
    fields = ["operation", "source", "handler", "boundary", "mcp", "permission", "protected_flow", "tests", "status"]
    previous = {}
    if LEDGER.exists():
        previous = {row['operation']: row for row in csv.DictReader(LEDGER.open(), delimiter='\t')}
    rows = []
    for key, row in sorted(actual.items()):
        entry = {**row, **{field: previous.get(key, {}).get(field, "pending") for field in fields[4:]}}
        rows.append(entry)
    ledger = encode(fields, rows)
    sites = web_sites()
    web = encode(["source", "line", "method_hint", "expression"], sites)
    if args.update:
        LEDGER.parent.mkdir(parents=True, exist_ok=True)
        LEDGER.write_text(ledger)
        WEB.write_text(web)
    else:
        if not LEDGER.exists() or LEDGER.read_text() != ledger:
            raise SystemExit("HTTP coverage ledger differs from registration; run --update and review every new/changed row")
        if not WEB.exists() or WEB.read_text() != web:
            raise SystemExit("Web call-site inventory differs; run --update and review")
    pending = [row for row in rows if row['status'] not in {'direct', 'equivalent', 'protected_handoff', 'transport_na', 'web_prohibited_na'} or any(row[key] == 'pending' for key in fields[4:])]
    print(f"HTTP operations: {len(rows)}; Web literal call sites: {len(sites)}; pending operations: {len(pending)}")
    if args.complete and pending:
        raise SystemExit("coverage is incomplete")


if __name__ == '__main__':
    main()
