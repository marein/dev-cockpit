// singleFlight makes the last word the one that stands: while a write is on the
// wire the next is only remembered, and it goes out when the first is through
// with whatever is current by then. Two writes can then never land in the wrong
// order, however short the wait in front of them gets. A call answers once a
// run that started after it is through, so a caller that awaits it reads what
// that run brought.
export function singleFlight(run) {
  let running = null;
  let again = null;
  const start = () => {
    if (running) {
      again ??= running.catch(() => {}).then(() => {
        again = null;
        return start();
      });
      return again;
    }
    running = (async () => {
      try {
        await run();
      } finally {
        running = null;
      }
    })();
    return running;
  };
  return start;
}
