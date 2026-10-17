// singleFlight makes the last word the one that stands: while a write is on the
// wire the next is only remembered, and it goes out when the first is through
// with whatever is current by then. Two writes can then never land in the wrong
// order, however short the wait in front of them gets.
export function singleFlight(run) {
  let running = false;
  let again = false;
  const start = async () => {
    if (running) {
      again = true;
      return;
    }
    running = true;
    try {
      await run();
    } finally {
      running = false;
      if (again) {
        again = false;
        void start();
      }
    }
  };
  return start;
}
