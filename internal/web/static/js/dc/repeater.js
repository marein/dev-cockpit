export function createRepeater(fire, initialDelay, repeatInterval) {
  let delayTimer = null;
  let repeatTimer = null;
  const stop = () => {
    if (delayTimer !== null) {
      window.clearTimeout(delayTimer);
      delayTimer = null;
    }
    if (repeatTimer !== null) {
      window.clearInterval(repeatTimer);
      repeatTimer = null;
    }
  };
  const start = () => {
    stop();
    fire();
    delayTimer = window.setTimeout(() => {
      repeatTimer = window.setInterval(fire, repeatInterval);
    }, initialDelay);
  };
  return { start, stop };
}
