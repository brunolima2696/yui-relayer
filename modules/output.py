import time
from contextlib import contextmanager
from datetime import datetime


def _timestamp() -> str:
    return datetime.now().astimezone().isoformat(timespec="seconds")


@contextmanager
def stage(name: str):
    started_at = time.monotonic()
    print(f"[{_timestamp()}] INICIO: {name}", flush=True)
    try:
        yield
    except Exception:
        elapsed = time.monotonic() - started_at
        print(f"[{_timestamp()}] ERRO: {name} ({elapsed:.1f}s)", flush=True)
        raise
    else:
        elapsed = time.monotonic() - started_at
        print(f"[{_timestamp()}] FIM: {name} ({elapsed:.1f}s)", flush=True)
