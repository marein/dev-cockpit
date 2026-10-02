import { getText } from "@dc/http";

// createPull fetches a server fragment for a live element: one request in
// flight, a burst behind it collapses into one more, and an answer that
// arrives after the element left the page is dropped.
export function createPull(url, apply, signal) {
  let inFlight = false;
  let dirty = false;
  const pull = () => {
    if (inFlight) {
      dirty = true;
      return;
    }
    inFlight = true;
    getText(url(), { signal })
      .then((html) => {
        if (!signal.aborted) apply(new DOMParser().parseFromString(html, "text/html"));
      })
      .catch(() => {})
      .finally(() => {
        inFlight = false;
        if (dirty && !signal.aborted) {
          dirty = false;
          pull();
        }
      });
  };
  return pull;
}
