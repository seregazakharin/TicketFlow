const state = {
  token: localStorage.getItem("ticketflow.token") || "",
  userID: localStorage.getItem("ticketflow.userID") || "",
  authMode: "register",
};

const authStatus = document.querySelector("#authStatus");
const authForm = document.querySelector("#authForm");
const nameField = document.querySelector("#nameField");
const authSubmitText = document.querySelector("#authSubmitText");
const eventsList = document.querySelector("#eventsList");
const notificationsList = document.querySelector("#notificationsList");
const apiLog = document.querySelector("#apiLog");

document.querySelector("#eventStartsAt").value = defaultStartsAt();
document.querySelector("#idempotencyKey").value = newIdempotencyKey();

document.querySelectorAll("[data-auth-mode]").forEach((button) => {
  button.addEventListener("click", () => {
    state.authMode = button.dataset.authMode;
    document.querySelectorAll("[data-auth-mode]").forEach((item) => item.classList.toggle("active", item === button));
    nameField.style.display = state.authMode === "register" ? "grid" : "none";
    authSubmitText.textContent = state.authMode === "register" ? "Создать пользователя" : "Войти";
  });
});

document.querySelector("#clearTokenBtn").addEventListener("click", () => {
  state.token = "";
  state.userID = "";
  localStorage.removeItem("ticketflow.token");
  localStorage.removeItem("ticketflow.userID");
  renderAuthStatus();
  log("Локальный токен очищен.");
});

document.querySelector("#refreshEventsBtn").addEventListener("click", loadEvents);
document.querySelector("#refreshNotificationsBtn").addEventListener("click", loadNotifications);
document.querySelector("#clearLogBtn").addEventListener("click", () => {
  apiLog.textContent = "Лог очищен.";
});

authForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const path = state.authMode === "register" ? "/users/register" : "/users/login";
  const body = {
    email: document.querySelector("#email").value.trim(),
    password: document.querySelector("#password").value,
  };
  if (state.authMode === "register") {
    body.name = document.querySelector("#name").value.trim();
  }
  const data = await api(path, { method: "POST", body });
  if (data.token) {
    state.token = data.token;
    state.userID = data.user_id || "";
    localStorage.setItem("ticketflow.token", state.token);
    localStorage.setItem("ticketflow.userID", state.userID);
    renderAuthStatus();
    await loadNotifications();
  }
});

document.querySelector("#eventForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const startsAt = new Date(document.querySelector("#eventStartsAt").value);
  const created = await api("/events", {
    method: "POST",
    body: {
      title: document.querySelector("#eventTitle").value.trim(),
      starts_at: startsAt.toISOString(),
      price_cents: Number(document.querySelector("#eventPrice").value),
      capacity: Number(document.querySelector("#eventCapacity").value),
    },
  });
  if (created.id) {
    document.querySelector("#orderEventID").value = created.id;
  }
  await loadEvents();
});

document.querySelector("#orderForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = await api("/orders", {
    method: "POST",
    auth: true,
    body: {
      event_id: document.querySelector("#orderEventID").value.trim(),
      quantity: Number(document.querySelector("#orderQuantity").value),
      idempotency_key: document.querySelector("#idempotencyKey").value.trim(),
    },
  });
  if (data.id) {
    document.querySelector("#idempotencyKey").value = newIdempotencyKey();
    await loadEvents();
    setTimeout(loadNotifications, 600);
  }
});

async function loadEvents() {
  const data = await api("/events", { method: "GET", quietErrors: true });
  const events = data.events || [];
  if (!events.length) {
    eventsList.innerHTML = `<div class="empty">Событий пока нет.</div>`;
    return;
  }
  eventsList.innerHTML = events.map(renderEvent).join("");
  eventsList.querySelectorAll("[data-select-event]").forEach((button) => {
    button.addEventListener("click", () => {
      document.querySelector("#orderEventID").value = button.dataset.selectEvent;
      document.querySelector("#idempotencyKey").value = newIdempotencyKey();
    });
  });
}

async function loadNotifications() {
  if (!state.token) {
    notificationsList.innerHTML = `<div class="empty">Войди, чтобы увидеть уведомления.</div>`;
    return;
  }
  const data = await api("/notifications", { method: "GET", auth: true, quietErrors: true });
  const items = data.notifications || [];
  if (!items.length) {
    notificationsList.innerHTML = `<div class="empty">Уведомлений пока нет.</div>`;
    return;
  }
  notificationsList.innerHTML = items.map((item) => `
    <div class="item">
      <p class="item-title">${escapeHTML(item.kind)}</p>
      <p class="item-meta">${escapeHTML(item.message)}</p>
      <p class="item-meta">${formatDate(item.created_at)}</p>
    </div>
  `).join("");
}

async function api(path, options = {}) {
  const headers = { Accept: "application/json" };
  const request = { method: options.method || "GET", headers };
  if (options.body) {
    headers["Content-Type"] = "application/json";
    request.body = JSON.stringify(options.body);
  }
  if (options.auth) {
    headers.Authorization = `Bearer ${state.token}`;
  }
  try {
    const response = await fetch(path, request);
    const text = await response.text();
    const data = text ? JSON.parse(text) : {};
    log(`${request.method} ${path} -> ${response.status}\n${JSON.stringify(data, null, 2)}`);
    if (!response.ok && !options.quietErrors) {
      throw new Error(data.error || `HTTP ${response.status}`);
    }
    return data;
  } catch (error) {
    log(`${request.method} ${path} -> error\n${error.message}`);
    return { error: error.message };
  }
}

function renderEvent(event) {
  return `
    <div class="item">
      <div class="item-head">
        <div>
          <p class="item-title">${escapeHTML(event.title)}</p>
          <p class="item-meta">${formatDate(event.starts_at)} · ${money(event.price_cents)}</p>
          <p class="item-meta">${escapeHTML(event.id)}</p>
        </div>
        <span class="badge">${event.available}/${event.capacity}</span>
      </div>
      <div class="item-actions">
        <button class="secondary" type="button" data-select-event="${escapeHTML(event.id)}">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 16.2l-3.5-3.5L4 14.2 9 19 20 8l-1.5-1.5L9 16.2z"/></svg>
          Выбрать
        </button>
      </div>
    </div>
  `;
}

function renderAuthStatus() {
  authStatus.textContent = state.token ? `Пользователь ${state.userID || "авторизован"}` : "Гость";
}

function log(message) {
  apiLog.textContent = `${new Date().toLocaleTimeString()}\n${message}\n\n${apiLog.textContent}`;
}

function defaultStartsAt() {
  const date = new Date();
  date.setDate(date.getDate() + 14);
  date.setHours(19, 0, 0, 0);
  return date.toISOString().slice(0, 16);
}

function newIdempotencyKey() {
  return `web-${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
}

function formatDate(value) {
  if (!value) {
    return "";
  }
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

function money(cents) {
  return new Intl.NumberFormat("ru-RU", {
    style: "currency",
    currency: "RUB",
  }).format((Number(cents) || 0) / 100);
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

renderAuthStatus();
loadEvents();
loadNotifications();
