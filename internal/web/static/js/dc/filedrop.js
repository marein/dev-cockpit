export function watchFileDrop(target, onDrop, { signal }) {
  const entered = new Set();
  const isFileDrag = (event) => Array.from(event.dataTransfer?.types || []).includes("Files");
  const settle = () => {
    for (const node of entered) {
      if (!target.contains(node)) entered.delete(node);
    }
    target.classList.toggle("dc-dropping", entered.size > 0);
  };
  const end = () => {
    entered.clear();
    target.classList.remove("dc-dropping");
  };
  target.addEventListener("dragenter", (event) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    entered.add(event.target);
    settle();
  }, { signal });
  target.addEventListener("dragover", (event) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  }, { signal });
  target.addEventListener("dragleave", (event) => {
    entered.delete(event.target);
    settle();
  }, { signal });
  target.addEventListener("drop", (event) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    end();
    const files = Array.from(event.dataTransfer.files || []);
    if (files.length) onDrop(files);
  }, { signal });
  document.addEventListener("dragend", end, { signal });
}
