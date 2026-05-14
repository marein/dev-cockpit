document.addEventListener("submit", (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || !form.dataset.confirm) {
    return;
  }
  if (form.dataset.confirmed === "true") {
    delete form.dataset.confirmed;
    return;
  }
  event.preventDefault();
  Swal.fire({
    title: form.dataset.confirm,
    icon: "warning",
    showCancelButton: true,
    confirmButtonText: form.dataset.confirmButton || "Confirm",
    cancelButtonText: "Cancel",
    reverseButtons: true,
    background: "#1f2937",
    color: "#f8fafc",
  }).then((result) => {
    if (!result.isConfirmed) {
      return;
    }
    form.dataset.confirmed = "true";
    form.requestSubmit();
  });
});
