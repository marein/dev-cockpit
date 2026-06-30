import { installErrorHandler } from "@dc/toast";
import { confirm } from "@dc/dialog";

installErrorHandler();

// Deletes a form's target via fetch and removes its row from the list instead of
// reloading the page. Falls back to a normal submit on any failure.
function ajaxDelete(form) {
  const row = form.closest(".list-group-item");
  const list = row ? row.parentElement : null;
  const card = form.closest('[id^="project-"]');
  fetch(form.action, {
    method: "POST",
    body: new URLSearchParams(new FormData(form)),
  })
    .then((response) => {
      if (!response.ok) throw new Error("delete failed");
      if (row) row.remove();
      // Drop the inner list once its last real entry is gone (a collapse toggle
      // doesn't count). Leaves the section header + "New" affordance, matching an
      // empty section's server render.
      if (list && list.querySelectorAll(".list-group-item:not([data-collapse-toggle])").length === 0) {
        list.remove();
      }
      if (card) document.dispatchEvent(new CustomEvent("dc:rendered", { detail: { root: card } }));
    })
    .catch(() => {
      form.dataset.confirmed = "true";
      form.requestSubmit();
    });
}

// Submits a form via fetch, then re-renders just its project card from the
// response (the projects page the POST redirects to). Used for actions that
// change a row rather than remove it (stopping a session -> it becomes
// inactive). Falls back to a normal submit on any failure.
function ajaxRefresh(form) {
  const card = form.closest('[id^="project-"]');
  fetch(form.action, {
    method: "POST",
    body: new URLSearchParams(new FormData(form)),
  })
    .then((response) => {
      if (!response.ok) throw new Error("submit failed");
      return response.text();
    })
    .then((html) => {
      const fresh = card
        ? new DOMParser().parseFromString(html, "text/html").getElementById(card.id)
        : null;
      if (!fresh || !card) throw new Error("card not found");
      card.replaceWith(fresh);
      document.dispatchEvent(new CustomEvent("dc:rendered", { detail: { root: fresh } }));
    })
    .catch(() => {
      form.dataset.confirmed = "true";
      form.requestSubmit();
    });
}

document.addEventListener("submit", async (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || !form.dataset.confirm) {
    return;
  }
  if (form.dataset.confirmed === "true") {
    delete form.dataset.confirmed;
    return;
  }
  event.preventDefault();
  const confirmed = await confirm({
    title: form.dataset.confirm,
    confirmText: form.dataset.confirmButton || "Confirm",
  });
  if (!confirmed) {
    return;
  }
  if (form.dataset.ajaxDelete !== undefined) {
    ajaxDelete(form);
    return;
  }
  if (form.dataset.ajaxRefresh !== undefined) {
    ajaxRefresh(form);
    return;
  }
  form.dataset.confirmed = "true";
  form.requestSubmit();
});
