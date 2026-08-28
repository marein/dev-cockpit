const slug = (raw) => String(raw).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
const repositoryName = (raw) => {
  const cleaned = String(raw).trim().replace(/\/+$/, "").replace(/\.git$/i, "");
  return cleaned.split(/[/:]/).pop() || "";
};

class ProjectNew extends HTMLElement {
  connectedCallback() {
    if (this.abort) {
      return;
    }
    this.abort = new AbortController();
    const signal = this.abort.signal;
    const source = this.querySelector("[data-project-source]");
    if (source) {
      source.addEventListener("change", () => {
        const picked = source.value;
        location.assign(picked ? `/projects/new?create=${encodeURIComponent(picked)}` : "/projects/new");
      }, { signal });
    }
    const name = this.querySelector("[data-project-name-field]");
    if (!name) {
      return;
    }
    this.typed = name.value.trim() !== "";
    name.addEventListener("input", () => {
      this.typed = name.value.trim() !== "";
    }, { signal });

    const cloneURL = this.querySelector("[data-clone-url]");
    if (cloneURL) {
      const fromURL = () => {
        if (this.typed) {
          return;
        }
        name.value = slug(repositoryName(cloneURL.value));
      };
      cloneURL.addEventListener("input", fromURL, { signal });
      fromURL();
    }

    const existing = this.querySelector("[data-branch-existing]");
    const created = this.querySelector("[data-branch-new]");
    const modes = Array.from(this.querySelectorAll("[data-branch-mode]"));
    if (!modes.length) {
      return;
    }

    const mode = () => {
      const picked = modes.find((radio) => radio.checked);
      return picked ? picked.value : "existing";
    };
    const branchName = () => {
      if (mode() === "new") {
        return created ? created.value : "";
      }
      const picked = existing && existing.selectedOptions[0];
      return picked ? picked.dataset.branchName || picked.value : "";
    };
    const suggest = () => {
      if (this.typed) {
        return;
      }
      const picked = source && source.selectedOptions[0];
      const project = picked ? picked.dataset.projectName || "" : "";
      const branch = slug(branchName());
      name.value = branch ? `${project}-${branch}` : project;
    };
    const apply = (focus) => {
      const active = mode();
      for (const block of this.querySelectorAll("[data-branch-block]")) {
        const on = block.dataset.branchBlock === active;
        block.hidden = !on;
        block.querySelectorAll("input, select").forEach((field) => {
          field.disabled = !on;
        });
      }
      if (focus && active === "new" && created) {
        created.focus();
      }
      suggest();
    };

    modes.forEach((radio) => radio.addEventListener("change", () => apply(true), { signal }));
    if (existing) {
      existing.addEventListener("change", suggest, { signal });
    }
    if (created) {
      created.addEventListener("input", suggest, { signal });
    }
    apply(false);
  }

  disconnectedCallback() {
    this.abort?.abort();
    this.abort = null;
  }
}

customElements.define("dc-project-new", ProjectNew);
