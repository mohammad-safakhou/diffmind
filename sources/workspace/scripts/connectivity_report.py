#!/usr/bin/env python3
"""Report architecture graph connectivity for a DiffMind run.

Input may be either a run directory containing graph.json or a graph.json path.
Repo failure data is read from repos/*/repo.json when present. For the current
DiffMind layout, project repos are also discovered from a sibling ../../repos
directory when the input is projects/<pid>/runs/<rid>.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any


PATH_RE = re.compile(r"(/(?:[^/\s:]+/)+[^/\s:]+)")
HEX_RE = re.compile(r"\b[0-9a-f]{8,40}\b", re.IGNORECASE)
RUN_RE = re.compile(r"\b20\d{6}T\d{6}Z\b")
ISO_TS_RE = re.compile(r"\b20\d{2}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z\b")
NUMBER_RE = re.compile(r"\b\d+\b")
SPACE_RE = re.compile(r"\s+")


def load_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def graph_path_from_input(path: Path) -> tuple[Path, Path]:
    if path.is_file():
        return path, path.parent
    return path / "graph.json", path


def service_names(graph: dict[str, Any]) -> set[str]:
    names: set[str] = set()
    for svc in graph.get("services") or []:
        if isinstance(svc, dict):
            name = str(svc.get("name") or svc.get("id") or "").strip()
            if name:
                names.add(name)
    return names


def edge_type(edge: dict[str, Any]) -> str:
    return str(edge.get("type") or "unknown").strip() or "unknown"


def endpoint(edge: dict[str, Any], key: str) -> str:
    return str(edge.get(key) or "").strip()


def queue_async_chains(edges: list[dict[str, Any]]) -> tuple[int, list[dict[str, Any]]]:
    publishers: dict[str, set[str]] = defaultdict(set)
    consumers: dict[str, set[str]] = defaultdict(set)
    subscriptions: dict[str, set[str]] = defaultdict(set)
    for edge in edges:
        typ = edge_type(edge)
        src = endpoint(edge, "from")
        dst = endpoint(edge, "to")
        if typ == "queue_publish" and dst:
            publishers[dst].add(src)
        elif typ == "queue_consume" and src:
            consumers[src].add(dst)
        elif typ == "queue_subscription" and src and dst:
            subscriptions[src].add(dst)
    # SNS fan-out: consumers of a subscribed queue are consumers of the topic.
    for topic, queues in subscriptions.items():
        for queue in queues:
            consumers[topic] |= consumers.get(queue, set())

    chains: list[dict[str, Any]] = []
    total = 0
    for queue in sorted(set(publishers) & set(consumers)):
        pubs = sorted(p for p in publishers[queue] if p)
        cons = sorted(c for c in consumers[queue] if c)
        count = len(pubs) * len(cons)
        if count == 0:
            continue
        total += count
        chains.append(
            {
                "queue": queue,
                "publishers": pubs,
                "consumers": cons,
                "chains": count,
            }
        )
    return total, chains


def candidate_repo_dirs(run_dir: Path) -> list[Path]:
    candidates = [run_dir / "repos"]
    if run_dir.parent.name == "runs":
        candidates.append(run_dir.parent.parent / "repos")
    seen: set[Path] = set()
    out: list[Path] = []
    for c in candidates:
        resolved = c.resolve()
        if resolved not in seen and c.is_dir():
            seen.add(resolved)
            out.append(c)
    return out


def normalize_error(raw: str) -> str:
	s = raw.strip()
	if not s:
		return "(empty error)"
	for marker in ("run failed:", "panic:", "fatal error:"):
		if marker in s:
			s = s.rsplit(marker, 1)[1].strip()
			break
	if "signal: killed" in s:
		s = "signal: killed"
	s = PATH_RE.sub("<path>", s)
	s = ISO_TS_RE.sub("<timestamp>", s)
	s = RUN_RE.sub("<run>", s)
	s = HEX_RE.sub("<hex>", s)
	s = re.sub(r'entrypoint object "[^"]+" must have at least one flow', 'entrypoint object "<object>" must have at least one flow', s)
	s = NUMBER_RE.sub("<n>", s)
	s = SPACE_RE.sub(" ", s)
	return s[:240]


def repo_failures(repo_dirs: list[Path]) -> tuple[int, Counter[str], list[dict[str, str]]]:
    signatures: Counter[str] = Counter()
    failures: list[dict[str, str]] = []
    total = 0
    for repo_dir in repo_dirs:
        for repo_json in sorted(repo_dir.glob("*/repo.json")):
            total += 1
            try:
                repo = load_json(repo_json)
            except (OSError, json.JSONDecodeError) as exc:
                sig = normalize_error(f"repo_json_read_error: {exc}")
                signatures[sig] += 1
                failures.append({"repo": repo_json.parent.name, "signature": sig})
                continue
            status = str(repo.get("sync_status") or "")
            err = str(repo.get("sync_error") or "")
            if status == "diffmind_failed" or err:
                sig = normalize_error(err or status)
                signatures[sig] += 1
                failures.append(
                    {
                        "repo": str(repo.get("name") or repo.get("id") or repo_json.parent.name),
                        "signature": sig,
                    }
                )
    return total, signatures, failures


def build_report(graph_path: Path, run_dir: Path) -> dict[str, Any]:
    graph = load_json(graph_path)
    services = service_names(graph)
    edges = [e for e in graph.get("edges") or [] if isinstance(e, dict)]
    edge_counts = Counter(edge_type(e) for e in edges)
    incident: set[str] = set()
    service_to_service = 0
    for edge in edges:
        src = endpoint(edge, "from")
        dst = endpoint(edge, "to")
        if src in services:
            incident.add(src)
        if dst in services:
            incident.add(dst)
        if src in services and dst in services:
            service_to_service += 1

    async_count, async_chains = queue_async_chains(edges)
    isolated = sorted(services - incident)
    repo_dirs = candidate_repo_dirs(run_dir)
    repo_total, failure_signatures, failures = repo_failures(repo_dirs)

    return {
        "graph": str(graph_path),
        "services": len(services),
        "edges_total": len(edges),
        "edge_counts": dict(sorted(edge_counts.items())),
        "service_to_service_edges": service_to_service,
        "async_chains": async_count,
        "async_chain_details": async_chains,
        "isolated_service_count": len(isolated),
        "isolated_services": isolated,
        "repo_json_count": repo_total,
        "repo_failure_count": len(failures),
        "repo_failure_histogram": dict(failure_signatures.most_common()),
        "repo_failure_details": failures,
    }


def print_text(report: dict[str, Any]) -> None:
    print(f"Graph: {report['graph']}")
    print(f"Services: {report['services']}")
    print(f"Edges: {report['edges_total']}")
    print(f"Service-to-service edges: {report['service_to_service_edges']}")
    print(f"Async chains: {report['async_chains']}")
    print(f"Isolated services: {len(report['isolated_services'])}")
    print()
    print("Edge counts by type:")
    for typ, count in report["edge_counts"].items():
        print(f"  {typ}: {count}")
    print()
    print("Repo failures by normalized signature:")
    if report["repo_failure_histogram"]:
        for sig, count in report["repo_failure_histogram"].items():
            print(f"  {count}x {sig}")
    else:
        print("  none")
    print()
    print("Async chain queues:")
    if report["async_chain_details"]:
        for item in report["async_chain_details"]:
            pubs = ", ".join(item["publishers"])
            cons = ", ".join(item["consumers"])
            print(f"  {item['queue']}: {item['chains']} ({pubs} -> {cons})")
    else:
        print("  none")
    print()
    print("Isolated service list:")
    if report["isolated_services"]:
        for svc in report["isolated_services"]:
            print(f"  {svc}")
    else:
        print("  none")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", help="Run directory or graph.json path")
    parser.add_argument("--json", action="store_true", help="Emit machine-readable JSON")
    args = parser.parse_args(argv)

    graph_path, run_dir = graph_path_from_input(Path(args.input).expanduser())
    if not graph_path.is_file():
        print(f"graph.json not found: {graph_path}", file=sys.stderr)
        return 2
    try:
        report = build_report(graph_path, run_dir)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"failed to build report: {exc}", file=sys.stderr)
        return 1
    if args.json:
        json.dump(report, sys.stdout, indent=2, sort_keys=True)
        print()
    else:
        print_text(report)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
