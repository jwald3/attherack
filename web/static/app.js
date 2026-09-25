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

  // ✨ AI fill: infer muscles/equipment/category from the exercise name
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
    const original = btn.textContent;
    btn.disabled = true;
    btn.textContent = "✨ …";
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
      const setVal = (sel, v) => {
        const el = document.querySelector(sel);
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
      btn.textContent = original;
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
    if (window.htmx) window.htmx.trigger(form, "submit");
  }
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
    if (!starter) return;
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
    if (!val || form.dataset.busy === "1") {
      e.preventDefault();
      return;
    }
    form.dataset.busy = "1";
    const empty = col.querySelector(".chat-empty");
    if (empty) empty.remove();
    // Show the message right away plus a "thinking" placeholder; the server
    // response re-renders both authoritatively.
    const mine = document.createElement("div");
    mine.className = "bubble user optimistic";
    mine.textContent = val;
    col.appendChild(mine);
    const pending = document.createElement("div");
    pending.className = "bubble assistant pending";
    pending.innerHTML = '<span class="typing"><i></i><i></i><i></i></span>';
    col.appendChild(pending);
    scroll();
    // Clear after htmx has serialized the form.
    setTimeout(() => {
      input.value = "";
      autosize(input);
    }, 0);
  });

  document.body.addEventListener("htmx:afterRequest", (e) => {
    if (e.target.id !== "chat-form") return;
    e.target.dataset.busy = "";
    document.querySelectorAll("#chat-col .pending, #chat-col .optimistic").forEach((el) => {
      if (!e.detail.successful) {
        // Keep the user's text visible and explain what happened.
        if (el.classList.contains("pending")) {
          el.classList.remove("pending");
          el.textContent = "Couldn't reach the server. Try again.";
        }
        return;
      }
      el.remove();
    });
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
