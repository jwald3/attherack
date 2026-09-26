// Small progressive-enhancement layer over HTMX.
(function () {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);

  // Populate the datalist for the "exercise" input on first focus, so the
  // add-set form autocompletes against the real exercise database.
  let namesLoaded = false;
  async function loadExerciseNames() {
    if (namesLoaded) return;
    namesLoaded = true;
    try {
      const res = await fetch("/exercises?query=");
      // We can't easily parse names from the fragment; instead pull a large
      // list and read data-name attributes.
      const html = await res.text();
      const tmp = document.createElement("div");
      tmp.innerHTML = html;
      const dl = $("#exercise-names");
      if (!dl) return;
      tmp.querySelectorAll(".ex-card").forEach((c) => {
        const opt = document.createElement("option");
        opt.value = c.getAttribute("data-name");
        dl.appendChild(opt);
      });
    } catch (e) {
      /* non-fatal */
    }
  }

  document.addEventListener("focusin", (e) => {
    if (e.target.matches('input[name="exercise"]')) loadExerciseNames();
  });

  // Exercise history drawer: click any exercise name to slide in its history.
  window.closeDrawer = function () {
    const d = document.getElementById("drawer");
    const b = document.getElementById("drawer-backdrop");
    if (d) {
      d.classList.remove("open");
      d.hidden = true;
    }
    if (b) b.hidden = true;
  };
  async function openDrawer(url) {
    const d = document.getElementById("drawer");
    const b = document.getElementById("drawer-backdrop");
    if (!d) return;
    b.hidden = false;
    d.hidden = false;
    d.innerHTML = '<div class="drawer-loading">Loading…</div>';
    // Force reflow so the slide-in transition runs.
    void d.offsetWidth;
    d.classList.add("open");
    try {
      const res = await fetch(url);
      d.innerHTML = await res.text();
    } catch (e) {
      d.innerHTML = '<div class="drawer-loading">Could not load history.</div>';
    }
  }
  document.addEventListener("click", (e) => {
    const link = e.target.closest(".ex-link");
    if (!link) return;
    e.preventDefault();
    const cardio = link.getAttribute("data-cardio-type");
    if (cardio) {
      openDrawer("/cardio/type?name=" + encodeURIComponent(cardio));
    } else {
      openDrawer("/exercise?name=" + encodeURIComponent(link.getAttribute("data-exercise-name")));
    }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") window.closeDrawer();
  });

  // "+ log" buttons on exercise cards fill the add-set form's exercise field.
  document.addEventListener("click", (e) => {
    const add = e.target.closest(".ex-add");
    if (!add) return;
    const name = add.getAttribute("data-exercise");
    const input = $('input[name="exercise"]');
    if (input) {
      input.value = name;
      input.focus();
      const weight = $('input[name="weight"]');
      if (weight) weight.focus();
    }
  });

  // Suggestion chips: clicking sets the matching filter field and re-runs the
  // search. Clicking the active chip again clears that filter. Only one chip
  // per field is active at a time.
  function syncClearButton() {
    const m = $("#ex-muscle"),
      eq = $("#ex-equipment");
    const btn = $("#facet-clear");
    if (btn) btn.hidden = !((m && m.value) || (eq && eq.value));
  }
  function runSearch() {
    const form = $("#exsearch");
    if (form && window.htmx) window.htmx.trigger(form, "refresh-search");
  }
  document.addEventListener("click", (e) => {
    const chip = e.target.closest(".chip.pick");
    if (chip) {
      const field = chip.getAttribute("data-field"); // "muscle" | "equipment"
      const val = chip.getAttribute("data-val");
      const input = document.getElementById("ex-" + field);
      if (!input) return;
      const active = input.value === val;
      input.value = active ? "" : val; // toggle off if it was already set
      // Update active styling within this chip's group.
      chip
        .closest(".facet-group")
        .querySelectorAll(".chip.pick")
        .forEach((c) => c.classList.remove("active"));
      if (!active) chip.classList.add("active");
      syncClearButton();
      runSearch();
      return;
    }
    // Mini-chips in the "add exercise" form append a muscle to a comma-list.
    const mini = e.target.closest(".mini-chip");
    if (mini) {
      const target = document.getElementById(mini.getAttribute("data-target"));
      const val = mini.getAttribute("data-val");
      if (target) {
        const parts = target.value
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean);
        if (parts.includes(val)) {
          // toggle off
          target.value = parts.filter((p) => p !== val).join(", ");
        } else {
          parts.push(val);
          target.value = parts.join(", ");
        }
        mini.classList.toggle("active");
        target.focus();
      }
      return;
    }

    if (e.target.id === "facet-clear") {
      ["ex-muscle", "ex-equipment"].forEach((id) => {
        const el = document.getElementById(id);
        if (el) el.value = "";
      });
      document
        .querySelectorAll(".chip.pick.active")
        .forEach((c) => c.classList.remove("active"));
      syncClearButton();
      runSearch();
    }
  });

  // AI fill: infer muscles/equipment/category from the exercise name
  // using a cheap model, then populate the add-exercise form.
  document.addEventListener("click", async (e) => {
    if (e.target.id !== "ai-fill") return;
    const btn = e.target;
    const name = ($("#add-name")?.value || "").trim();
    const result = $("#ex-add-result");
    if (!name) {
      if (result) result.innerHTML = '<div class="ex-add-msg err">Enter a name first.</div>';
      $("#add-name")?.focus();
      return;
    }
    btn.disabled = true;
    btn.classList.add("loading");
    if (result) result.innerHTML = '<div class="ex-add-msg">Thinking…</div>';
    try {
      const res = await fetch("/exercises/suggest", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: "name=" + encodeURIComponent(name),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        if (result) result.innerHTML = '<div class="ex-add-msg err">' + (data.error || "AI fill failed.") + "</div>";
        return;
      }
      // Scope to the add form: the library search above also has an
      // "equipment" field, which a page-wide lookup would find first.
      const form = btn.closest(".ex-add-form");
      const setVal = (sel, v) => {
        const el = form?.querySelector(sel);
        if (el && v != null && v !== "") el.value = v;
      };
      setVal('[name="primary_muscles"]', (data.primary_muscles || []).join(", "));
      setVal('[name="secondary_muscles"]', (data.secondary_muscles || []).join(", "));
      setVal('[name="equipment"]', data.equipment);
      setVal('[name="category"]', data.category);
      if (result) result.innerHTML = '<div class="ex-add-msg ok">Filled from AI — review, then Add.</div>';
    } catch (err) {
      if (result) result.innerHTML = '<div class="ex-add-msg err">AI fill failed.</div>';
    } finally {
      btn.disabled = false;
      btn.classList.remove("loading");
    }
  });

  // ---- Coach chat ----
  const scroll = () => {
    const box = $("#chat-scroll");
    if (box) box.scrollTop = box.scrollHeight;
  };
  document.addEventListener("DOMContentLoaded", scroll);

  // Grow the composer with its content (up to the CSS max-height).
  function autosize(el) {
    el.style.height = "auto";
    el.style.height = el.scrollHeight + "px";
  }
  function sendChat() {
    const form = $("#chat-form");
    if (!form || form.dataset.busy === "1" || form.dataset.disabled === "1") return;
    if (attachments.some((a) => a.busy)) return; // still downscaling a photo
    if (window.htmx) window.htmx.trigger(form, "submit");
  }

  // ---- Photo attachments ----
  // Photos are downscaled in the browser (longest edge 1568px, JPEG) before
  // upload: phone photos are often 4-8 MB, the API caps images at 5 MB, and
  // anything larger than ~1.15 megapixels is resized server-side anyway. The
  // resized files are written back into the hidden <input type=file> so HTMX
  // uploads them with the rest of the form.
  const MAX_ATTACH = 4;
  const MAX_EDGE = 1568;
  const attachments = []; // {id, file, url, busy}
  let attachSeq = 0;

  function syncFileInput() {
    const input = $("#chat-files");
    if (!input) return;
    try {
      const dt = new DataTransfer();
      attachments.forEach((a) => {
        if (!a.busy && a.file) dt.items.add(a.file);
      });
      input.files = dt.files;
    } catch (e) {
      /* very old browsers: leave the picker's own files in place */
    }
  }

  function renderPreviews() {
    const box = $("#chat-previews");
    if (!box) return;
    box.innerHTML = "";
    box.hidden = attachments.length === 0;
    attachments.forEach((a) => {
      const wrap = document.createElement("div");
      wrap.className = "chat-preview" + (a.busy ? " busy" : "");
      const img = document.createElement("img");
      img.src = a.url;
      img.alt = "";
      const rm = document.createElement("button");
      rm.type = "button";
      rm.className = "remove";
      rm.title = "Remove";
      rm.setAttribute("aria-label", "Remove photo");
      rm.innerHTML =
        '<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>';
      rm.addEventListener("click", () => removeAttachment(a.id));
      wrap.append(img, rm);
      box.appendChild(wrap);
    });
  }

  function removeAttachment(id) {
    const i = attachments.findIndex((a) => a.id === id);
    if (i < 0) return;
    URL.revokeObjectURL(attachments[i].url);
    attachments.splice(i, 1);
    renderPreviews();
    syncFileInput();
  }

  function clearAttachments() {
    attachments.forEach((a) => URL.revokeObjectURL(a.url));
    attachments.length = 0;
    renderPreviews();
    syncFileInput();
  }

  function loadImage(url) {
    return new Promise((resolve, reject) => {
      const img = new Image();
      img.onload = () => resolve(img);
      img.onerror = reject;
      img.src = url;
    });
  }

  // Returns a File no larger than MAX_EDGE on its longest side. Small JPEG/PNG/
  // WebP/GIF files pass through untouched; everything else (including HEIC on
  // browsers that can decode it) is re-encoded as JPEG.
  async function shrinkImage(file, url) {
    const passthrough = ["image/jpeg", "image/png", "image/webp", "image/gif"];
    let img;
    try {
      img = await loadImage(url);
    } catch (e) {
      return file; // undecodable here; let the server decide
    }
    const w = img.naturalWidth || img.width;
    const h = img.naturalHeight || img.height;
    const scale = Math.min(1, MAX_EDGE / Math.max(w, h));
    if (scale === 1 && passthrough.includes(file.type) && file.size <= 1.5 * 1024 * 1024) return file;
    if (file.type === "image/gif" && file.size <= 4 * 1024 * 1024) return file; // keep animation
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(w * scale);
    canvas.height = Math.round(h * scale);
    const ctx = canvas.getContext("2d");
    if (!ctx) return file;
    ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise((res) => canvas.toBlob(res, "image/jpeg", 0.86));
    if (!blob) return file;
    const name = (file.name || "photo").replace(/\.[^.]+$/, "") + ".jpg";
    return new File([blob], name, { type: "image/jpeg" });
  }

  async function addFiles(files) {
    const form = $("#chat-form");
    if (!form || form.dataset.disabled === "1") return;
    for (const file of Array.from(files || [])) {
      if (!file || (!file.type.startsWith("image/") && !/\.(heic|heif)$/i.test(file.name || ""))) continue;
      if (attachments.length >= MAX_ATTACH) {
        showChatNotice("You can attach up to " + MAX_ATTACH + " photos per message.");
        break;
      }
      const entry = { id: ++attachSeq, file: null, url: URL.createObjectURL(file), busy: true };
      attachments.push(entry);
      renderPreviews();
      try {
        entry.file = await shrinkImage(file, entry.url);
      } catch (e) {
        entry.file = file;
      }
      entry.busy = false;
      if (attachments.includes(entry)) {
        renderPreviews();
        syncFileInput();
      }
    }
    $("#chat-input")?.focus();
  }

  // Brief inline notice under the composer (limits, upload errors).
  function showChatNotice(text) {
    const hint = $(".composer-hint");
    if (!hint) return;
    if (!hint.dataset.orig) hint.dataset.orig = hint.textContent;
    hint.textContent = text;
    hint.classList.add("chat-err");
    clearTimeout(showChatNotice.t);
    showChatNotice.t = setTimeout(() => {
      hint.textContent = hint.dataset.orig;
      hint.classList.remove("chat-err");
    }, 4000);
  }

  document.addEventListener("click", (e) => {
    if (e.target.closest("#chat-attach") || e.target.closest(".starter-photo")) {
      const input = $("#chat-files");
      if (input && !input.disabled) input.click();
    }
  });
  document.addEventListener("change", (e) => {
    // Styled file inputs (.filepick): show the chosen filename(s) next to the
    // custom button.
    if (e.target.matches?.('.filepick input[type="file"]')) {
      const label = e.target.closest(".filepick")?.querySelector(".filepick-name");
      if (label) {
        const files = Array.from(e.target.files || []);
        label.textContent =
          files.length === 0 ? "" :
          files.length === 1 ? files[0].name :
          files.length + " files selected";
      }
      return;
    }
    if (e.target.id !== "chat-files") return;
    // The picker's own selection is replaced by the downscaled copies once
    // they're ready, so grab the originals first.
    const picked = Array.from(e.target.files || []);
    syncFileInput();
    addFiles(picked);
  });
  // Paste a screenshot or a copied image straight into the composer.
  document.addEventListener("paste", (e) => {
    if (e.target.id !== "chat-input") return;
    const items = Array.from(e.clipboardData?.items || []);
    const files = items.filter((it) => it.kind === "file" && it.type.startsWith("image/")).map((it) => it.getAsFile());
    if (files.length) {
      e.preventDefault();
      addFiles(files);
    }
  });
  // Drag a photo onto the composer.
  document.addEventListener("dragover", (e) => {
    const c = e.target.closest?.("#chat-form");
    if (!c) return;
    e.preventDefault();
    c.classList.add("dragover");
  });
  document.addEventListener("dragleave", (e) => {
    const c = e.target.closest?.("#chat-form");
    if (c && !c.contains(e.relatedTarget)) c.classList.remove("dragover");
  });
  document.addEventListener("drop", (e) => {
    const c = e.target.closest?.("#chat-form");
    if (!c) return;
    e.preventDefault();
    c.classList.remove("dragover");
    addFiles(e.dataTransfer?.files);
  });
  document.addEventListener("input", (e) => {
    if (e.target.id === "chat-input") autosize(e.target);
  });
  document.addEventListener("keydown", (e) => {
    if (e.target.id === "chat-input" && e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      sendChat();
    }
  });
  // Starter prompts on an empty chat send immediately.
  document.addEventListener("click", (e) => {
    const starter = e.target.closest(".starter");
    if (!starter || starter.classList.contains("starter-photo")) return;
    const input = $("#chat-input");
    input.value = starter.textContent.trim();
    sendChat();
  });

  document.body.addEventListener("htmx:beforeRequest", (e) => {
    if (e.target.id !== "chat-form") return;
    const form = e.target;
    const input = $("#chat-input");
    const col = $("#chat-col");
    const val = (input.value || "").trim();
    const ready = attachments.filter((a) => !a.busy && a.file);
    if ((!val && ready.length === 0) || form.dataset.busy === "1" || attachments.some((a) => a.busy)) {
      e.preventDefault();
      return;
    }
    form.dataset.busy = "1";
    form.dataset.lastText = val;
    const empty = col.querySelector(".chat-empty");
    if (empty) empty.remove();
    // Show the user's message right away. The server response re-renders it
    // authoritatively and appends its own polling "thinking" placeholder, so we
    // don't add a pending bubble here (that now comes from the server and keeps
    // polling until the background reply lands).
    const mine = document.createElement("div");
    mine.className = "bubble user optimistic";
    if (ready.length) {
      const strip = document.createElement("div");
      strip.className = "bubble-images";
      ready.forEach((a) => {
        const img = document.createElement("img");
        img.src = a.url;
        img.alt = "";
        strip.appendChild(img);
      });
      mine.appendChild(strip);
    }
    mine.appendChild(document.createTextNode(val));
    col.appendChild(mine);
    scroll();
    // Clear after htmx has serialized the form. Object URLs stay alive until
    // the optimistic bubble is replaced by the server's copy.
    setTimeout(() => {
      input.value = "";
      autosize(input);
      attachments.length = 0;
      renderPreviews();
      syncFileInput();
    }, 0);
  });

  document.body.addEventListener("htmx:afterRequest", (e) => {
    if (e.target.id !== "chat-form") return;
    e.target.dataset.busy = "";
    if (!e.detail.successful) {
      // The POST itself failed: a rejected upload (4xx with a message) or an
      // unreachable server. Drop the optimistic bubble, restore the text so
      // nothing is lost, and say what went wrong.
      const xhr = e.detail.xhr;
      const msg =
        xhr && xhr.status >= 400 && xhr.status < 500 && xhr.responseText && xhr.responseText.length < 300
          ? xhr.responseText.trim()
          : "Couldn't reach the server. Try again.";
      document.querySelectorAll("#chat-col .optimistic").forEach((el) => {
        el.querySelectorAll("img").forEach((img) => URL.revokeObjectURL(img.src));
        el.remove();
      });
      const input = $("#chat-input");
      if (input && !input.value) {
        input.value = e.target.dataset.lastText || "";
        autosize(input);
      }
      showChatNotice(msg);
    } else {
      // Success: the server appended the authoritative user bubble and a polling
      // placeholder, so drop our optimistic copy of the user's message.
      document.querySelectorAll("#chat-col .optimistic").forEach((el) => {
        el.querySelectorAll("img").forEach((img) => URL.revokeObjectURL(img.src));
        el.remove();
      });
    }
    $("#chat-input")?.focus();
    scroll();
  });

  // Keep the header title in sync with the sidebar (new chats get a title
  // from the server after the first reply; renames update it too).
  function syncTitle() {
    const active = $(".thread.active .thread-link");
    const h = $(".coach-title");
    if (active && h) {
      h.textContent = active.textContent;
      document.title = "At The Rack — " + active.textContent;
    }
  }
  document.body.addEventListener("htmx:oobAfterSwap", syncTitle);
  document.body.addEventListener("htmx:afterSwap", (e) => {
    if (e.target.id === "thread-list") syncTitle();
  });

  document.addEventListener("click", (e) => {
    const btn = e.target.closest(".thread-rename");
    if (!btn) return;
    const title = window.prompt("Rename conversation", btn.dataset.title);
    if (!title || !title.trim() || !window.htmx) return;
    window.htmx.ajax("POST", "/threads/" + btn.dataset.id + "/rename", {
      target: "#thread-list",
      swap: "outerHTML",
      values: { title: title.trim(), current: btn.dataset.current },
    });
  });

  // When the API key changes, the coach's enabled state (chat input, empty
  // note, "key needed" tag) needs to flip. Reload after a short beat so the
  // user sees the inline success message first.
  document.body.addEventListener("settings-changed", () => {
    setTimeout(() => window.location.reload(), 700);
  });

  // ---- Programs ----
  // Clone the last exercise row when "+ Add exercise" is clicked. Keeps the
  // exercise autocomplete (name="exercise" + list="exercise-names") intact.
  document.addEventListener("click", (e) => {
    if (e.target.closest("#prog-add-row")) {
      const rows = $("#prog-rows");
      if (!rows) return;
      const last = rows.querySelector(".prog-row:last-child");
      const clone = last.cloneNode(true);
      clone.querySelectorAll("input").forEach((inp) => {
        // Keep the default sets value; clear everything else.
        inp.value = inp.name === "sets" ? "3" : "";
      });
      rows.appendChild(clone);
      clone.querySelector('input[name="exercise"]')?.focus();
      return;
    }
    // Remove a row (but never the last remaining one — just clear it instead).
    const del = e.target.closest(".prog-row-del");
    if (del) {
      const rows = $("#prog-rows");
      const row = del.closest(".prog-row");
      if (rows && rows.querySelectorAll(".prog-row").length > 1) {
        row.remove();
      } else {
        row.querySelectorAll("input").forEach((inp) => {
          inp.value = inp.name === "sets" ? "3" : "";
        });
      }
    }
  });

  // After a program is saved, reset the form back to a single blank row.
  document.body.addEventListener("program-added", () => {
    const form = $("#prog-form");
    if (!form) return;
    form.reset();
    const rows = $("#prog-rows");
    if (rows) {
      const first = rows.querySelector(".prog-row");
      rows.querySelectorAll(".prog-row").forEach((r, i) => {
        if (i > 0) r.remove();
      });
      first?.querySelectorAll("input").forEach((inp) => {
        inp.value = inp.name === "sets" ? "3" : "";
      });
    }
    $("#prog-name")?.focus();
  });

  // Supplement unit: a styled <select> drives the actual name="unit" text input.
  // Picking a listed unit copies it into the (hidden) input; "Other…" reveals the
  // input for a custom value. This keeps one submitted field, no server change.
  function syncSuppUnit(sel) {
    const custom = $("#supp-unit-custom");
    if (!custom) return;
    if (sel.value === "__other") {
      custom.hidden = false;
      custom.value = "";
      custom.focus();
    } else {
      custom.hidden = true;
      custom.value = sel.value; // "" for the "Unit" placeholder option
    }
  }
  document.addEventListener("change", (e) => {
    if (e.target.id === "supp-unit-select") syncSuppUnit(e.target);
  });

  // After a progress photo uploads, reset the form and clear the filename readout.
  document.body.addEventListener("photo-added", () => {
    const form = $("#photo-form");
    if (!form) return;
    form.reset();
    const name = form.querySelector(".filepick-name");
    if (name) name.textContent = "";
  });

  // When a custom exercise is added, reset the form, refresh search results so
  // it appears, and invalidate the cached datalist so it autocompletes too.
  document.body.addEventListener("exercise-added", () => {
    const form = $(".ex-add-form");
    if (form) {
      const name = form.querySelector('input[name="name"]');
      form.reset();
      form.querySelectorAll(".mini-chip.active").forEach((c) => c.classList.remove("active"));
      if (name) name.focus();
    }
    namesLoaded = false;
    const dl = $("#exercise-names");
    if (dl) dl.innerHTML = "";
    // Re-run whatever search is currently in the box so the new card shows.
    const exForm = $(".exsearch");
    if (exForm && window.htmx) window.htmx.trigger(exForm, "submit");
  });
})();
