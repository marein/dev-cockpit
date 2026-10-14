let offset = 0;

export async function measureServerClock() {
  try {
    const sent = Date.now();
    const response = await fetch("/time", { cache: "no-store", headers: { Accept: "application/json" } });
    const back = Date.now();
    if (!response.ok) return;
    const { now } = await response.json();
    if (Number.isFinite(now)) offset = now - (sent + back) / 2;
  } catch {
    void 0;
  }
}

export function serverNow() {
  return Date.now() + offset;
}
