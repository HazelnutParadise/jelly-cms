(() => {
  "use strict";
  const root = document.getElementById("agentsPage");
  if (!root) return;
  const en = root.dataset.lang === "en";
  const tr = (zh, english) => (en ? english : zh);
  const $ = (id) => document.getElementById(id);
  const node = (tag, text, cls) => {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  };
  const button = (text, fn, kind = "secondary") => {
    const b = node("button", text, "btn btn-" + kind);
    b.type = "button";
    b.addEventListener("click", fn);
    return b;
  };
  const link = (text, href) => {
    const a = node("a", text);
    a.href = href;
    return a;
  };
  const status = $("agentsStatus"),
    content = $("agentsContent"),
    dialog = $("agentDialog");
  const id =
    root.dataset.view === "detail"
      ? location.pathname.split("/").filter(Boolean).pop()
      : null;
  const endpoint =
    "/api/admin/agents" + (id ? "/" + encodeURIComponent(id) : "");
  const graceError = tr(
    "過渡期必須是 1 到 1440 分鐘（最長 24 小時）",
    "Grace period must be 1 to 1440 minutes (at most 24 hours).",
  );
  let permissions = [],
    agents = [],
    current = null,
    busy = false,
    confirmationPending = false,
    secret = "",
    returnFocus = null,
    epoch = 0,
    pageActive = true;
  const controllers = new Set();
  const roles = {
    admin: tr("管理員", "Administrator"),
    editor: tr("編輯", "Editor"),
    author: tr("作者", "Author"),
    customer: tr("顧客", "Customer"),
  };
  const resources = {
    posts: tr("文章", "Posts"),
    pages: tr("頁面", "Pages"),
    media: tr("附件", "Media"),
    products: tr("商品", "Products"),
    orders: tr("訂單", "Orders"),
    themes: tr("主題", "Themes"),
    plugins: tr("外掛", "Plugins"),
    settings: tr("設定", "Settings"),
    logs: tr("紀錄", "Logs"),
  };
  const labels = {
    active: tr("啟用", "Active"),
    disabled: tr("停用", "Disabled"),
    revoked: tr("已撤銷", "Revoked"),
    expired: tr("已過期", "Expired"),
    grace: tr("過渡中", "Grace period"),
  };
  const date = (value) => {
    if (!value) return "—";
    const d = new Date(value);
    return Number.isNaN(d.getTime())
      ? "—"
      : new Intl.DateTimeFormat(en ? "en" : "zh-TW", {
          dateStyle: "medium",
          timeStyle: "short",
        }).format(d);
  };
  const message = (target, text, error = false) => {
    target.textContent = text;
    target.classList.toggle("agent-error", error);
  };
  function errorText(body, response) {
    if (body && body.code === "grace_out_of_range") return graceError;
    const codes = {
      name_invalid: tr(
        "名稱必須是 1 到 80 個字。",
        "Name must contain 1–80 characters.",
      ),
      description_too_long: tr(
        "說明最多 500 個字。",
        "Description must be at most 500 characters.",
      ),
      permissions_required: tr(
        "請至少選擇一項權限。",
        "Select at least one permission.",
      ),
      agent_name_taken: tr(
        "這個名稱已被使用，請換一個。",
        "This name is already in use.",
      ),
      agent_not_found: tr("找不到這個 agent。", "Agent not found."),
      forbidden: tr("你沒有管理 agent 的權限。", "You cannot manage agents."),
    };
    if (body && codes[body.code]) return codes[body.code];
    return (
      tr("請求失敗，請重試。", "Request failed. Please retry.") +
      (response ? " (HTTP " + response.status + ")" : "")
    );
  }
  async function api(path, method = "GET", body) {
    const controller = new AbortController();
    controllers.add(controller);
    const timeout = setTimeout(() => controller.abort(), 20000);
    try {
      const options = {
        method,
        mode: "same-origin",
        credentials: "same-origin",
        cache: "no-store",
        signal: controller.signal,
        headers: { Accept: "application/json" },
      };
      if (method !== "GET") options.headers["X-Jelly-CSRF"] = "1";
      if (body !== undefined) {
        options.headers["Content-Type"] = "application/json";
        options.body = JSON.stringify(body);
      }
      const response = await fetch(path, options);
      let data = null;
      if (response.status !== 204) {
        try {
          data = await response.json();
        } catch (_) {}
      }
      if (!response.ok) throw new Error(errorText(data, response));
      if (response.status !== 204 && !data)
        throw new Error(tr("伺服器回應格式錯誤。", "Invalid server response."));
      return data;
    } catch (e) {
      if (e instanceof TypeError || e.name === "AbortError")
        throw new Error(
          tr(
            "連線中斷或逾時。輸入已保留，請確認狀態後手動重試，避免重複操作。",
            "Connection lost or timed out. Your input is preserved. Check the current state before retrying to avoid repeating an operation.",
          ),
        );
      throw e;
    } finally {
      clearTimeout(timeout);
      controllers.delete(controller);
    }
  }
  function setBusy(value) {
    busy = value;
    root.querySelectorAll("[data-mutation]").forEach((b) => {
      b.disabled = value || b.dataset.invalid === "true";
    });
    $("agentDialogClose").disabled = value;
  }
  async function mutate(
    path,
    method,
    payload,
    done,
    target = $("agentDialogStatus"),
  ) {
    if (busy || !pageActive) return;
    const mine = epoch;
    setBusy(true);
    message(target, tr("處理中…", "Working…"));
    try {
      const result = await api(path, method, payload);
      if (mine === epoch && pageActive) await done(result);
    } catch (e) {
      if (mine === epoch && pageActive) message(target, e.message, true);
    } finally {
      if (mine === epoch && pageActive) setBusy(false);
    }
  }
  function mutationButton(text, fn, kind) {
    const b = button(text, fn, kind);
    b.dataset.mutation = "";
    b.disabled = busy;
    return b;
  }
  function purgeSecret() {
    secret = "";
    const box = $("agentSecret");
    if (box) {
      box.textContent = "";
      box.remove();
    }
  }
  function closeDialog(force = false) {
    if (busy && !force) return;
    purgeSecret();
    dialog.close();
    $("agentDialogBody").replaceChildren();
    $("agentDialogActions").replaceChildren();
    $("agentDialogStatus").textContent = "";
    if (returnFocus && returnFocus.isConnected && !force) returnFocus.focus();
  }
  function openDialog(title, body, actions) {
    purgeSecret();
    if (!dialog.open) returnFocus = document.activeElement;
    $("agentDialogTitle").textContent = title;
    $("agentDialogBody").replaceChildren(body);
    $("agentDialogActions").replaceChildren(...actions);
    $("agentDialogStatus").textContent = "";
    if (!dialog.open) dialog.showModal();
    const first = dialog.querySelector(
      "input:not([disabled]),button:not([disabled])",
    );
    if (first) first.focus();
  }
  const cancel = () => button(tr("取消", "Cancel"), () => closeDialog());
  function showSecret(result) {
    const value = result.api_key;
    result.api_key = "";
    if (typeof value !== "string" || !value.startsWith("jla_"))
      throw new Error(
        tr(
          "金鑰回應不完整，請確認清單後輪替金鑰。",
          "The key response is incomplete. Check the list and rotate the key.",
        ),
      );
    const box = node("div", undefined, "agent-keybox");
    box.append(
      node(
        "p",
        tr(
          "請立即保存金鑰。關閉或離開後，無法再次查看。",
          "Save this key now. You cannot view it after closing or leaving.",
        ),
      ),
    );
    const code = node("code", value);
    code.id = "agentSecret";
    box.append(code);
    const copied = node("p", "");
    copied.setAttribute("role", "status");
    const copy = button(tr("複製金鑰", "Copy key"), async () => {
      try {
        await navigator.clipboard.writeText(secret);
        if (secret)
          message(copied, tr("已複製到剪貼簿。", "Copied to clipboard."));
      } catch (_) {
        message(
          copied,
          tr(
            "無法使用剪貼簿，請選取上方金鑰手動複製。",
            "Clipboard unavailable. Select and copy the key above.",
          ),
          true,
        );
      }
    });
    box.append(copy, copied, node("p", "Authorization: Bearer jla_…"));
    if (result.previous_key)
      box.append(
        node(
          "p",
          result.previous_key.expires_at
            ? tr("舊金鑰到期：", "Previous key expires: ") +
                date(result.previous_key.expires_at)
            : tr(
                "舊金鑰已立即撤銷。",
                "The previous key was revoked immediately.",
              ),
        ),
      );
    openDialog(tr("金鑰只會顯示這一次", "Your key is shown only once"), box, [
      button(
        tr("我已複製，關閉", "I have copied it, close"),
        () => closeDialog(),
        "primary",
      ),
    ]);
    secret = value;
  }
  function field(label, name, value = "", multiline = false) {
    const wrap = node("label", label),
      input = node(multiline ? "textarea" : "input");
    input.name = name;
    input.id = "agent-" + name;
    input.value = value;
    if (!multiline) input.type = "text";
    else input.rows = 3;
    wrap.append(input);
    return { wrap, input };
  }
  function permissionGrid(selected) {
    const grid = node("fieldset", undefined, "agent-permissions");
    grid.append(node("legend", tr("權限", "Permissions")));
    const head = node("div", undefined, "agent-perm-row");
    [tr("資源", "Resource"), tr("讀取", "Read"), tr("寫入", "Write")].forEach(
      (x) => head.append(node("span", x)),
    );
    grid.append(head);
    const groups = new Map();
    permissions.forEach((p) => {
      const key = p.name.split(".")[0];
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(p);
    });
    groups.forEach((items, res) => {
      const row = node("div", undefined, "agent-perm-row"),
        title = node("span", resources[res] || res);
      title.append(node("small", res + ".*", "agent-muted"));
      row.append(title);
      ["read", "write"].forEach((op) => {
        const p = items.find((x) => x.name === res + "." + op);
        if (!p) {
          row.append(node("span", "—"));
          return;
        }
        const label = node("label"),
          check = node("input");
        check.type = "checkbox";
        check.name = "permission";
        check.value = p.name;
        check.checked = selected.includes(p.name);
        check.setAttribute("aria-label", p.name);
        label.append(check);
        if (
          p.required_roles &&
          p.required_roles.length === 1 &&
          p.required_roles[0] === "admin"
        )
          label.append(node("small", tr("需管理員", "Admin only")));
        row.append(label);
      });
      grid.append(row);
    });
    return grid;
  }
  function editor(agent, creating) {
    const form = node("form", undefined, "agent-form"),
      name = field(tr("名稱", "Name"), "name", agent ? agent.name : ""),
      desc = field(
        tr("說明（可選）", "Description (optional)"),
        "description",
        agent ? agent.description : "",
        true,
      );
    name.input.required = true;
    name.input.autocomplete = "off";
    form.append(
      name.wrap,
      node(
        "small",
        tr("1–80 字，名稱不能重複。", "1–80 characters. Names must be unique."),
        "agent-muted",
      ),
      desc.wrap,
      permissionGrid(agent ? agent.permissions : []),
    );
    const feedback = node("p", "", "agent-error");
    feedback.setAttribute("role", "status");
    const save = mutationButton(
      creating
        ? tr("建立並產生金鑰", "Create and generate key")
        : tr("儲存變更", "Save changes"),
      () => form.requestSubmit(),
      "primary",
    );
    form.append(feedback, save);
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      if (busy) return;
      const payload = {
        name: name.input.value.trim(),
        description: desc.input.value,
        permissions: [
          ...form.querySelectorAll("[name=permission]:checked"),
        ].map((x) => x.value),
      };
      if ([...payload.name].length < 1 || [...payload.name].length > 80) {
        message(
          feedback,
          tr("名稱必須是 1 到 80 個字。", "Name must contain 1–80 characters."),
          true,
        );
        return;
      }
      if ([...payload.description].length > 500) {
        message(
          feedback,
          tr(
            "說明最多 500 個字。",
            "Description must be at most 500 characters.",
          ),
          true,
        );
        return;
      }
      if (!payload.permissions.length) {
        message(
          feedback,
          tr("請至少選擇一項權限。", "Select at least one permission."),
          true,
        );
        return;
      }
      mutate(
        creating ? "/api/admin/agents" : endpoint,
        creating ? "POST" : "PATCH",
        payload,
        async (result) => {
          if (creating) {
            form.reset();
            agents.unshift(result.agent);
            renderListRows();
            showSecret(result);
          } else {
            current = result;
            refreshDetailSummary();
            message(feedback, tr("已儲存。", "Saved."));
          }
        },
        feedback,
      );
    });
    return form;
  }
  function table(headers) {
    const wrap = node("div", undefined, "agent-table-wrap"),
      t = node("table", undefined, "agent-table"),
      head = node("thead"),
      r = node("tr"),
      body = node("tbody");
    headers.forEach((h) => r.append(node("th", h)));
    head.append(r);
    t.append(head, body);
    wrap.append(t);
    return { wrap, body, headers };
  }
  function row(t, values) {
    const r = node("tr");
    values.forEach((v, i) => {
      const td = node("td");
      td.dataset.label = t.headers[i] || "";
      td.append(v instanceof Node ? v : node("span", v));
      r.append(td);
    });
    t.body.append(r);
  }
  function chip(text, kind = "") {
    return node("span", text, "badge" + (kind ? " badge-" + kind : ""));
  }
  function effective(a) {
    return (
      tr("有效權限 ", "Effective permissions ") +
      (a.effective_permissions || []).length +
      "/" +
      (a.permissions || []).length
    );
  }
  function owner(a) {
    return a.owner
      ? a.owner.username + " · " + (roles[a.owner.role] || a.owner.role)
      : tr("擁有者已刪除", "Owner deleted");
  }
  function keyState(k) {
    if (k.revoked_at) return "revoked";
    if (k.expires_at)
      return new Date(k.expires_at).getTime() <= Date.now()
        ? "expired"
        : "grace";
    return k.status || "active";
  }
  function keyBadge(k) {
    const s = keyState(k);
    let text = s === "active" ? tr("生效中", "Active") : labels[s] || s;
    if (s === "grace")
      text +=
        tr(" · 剩 ", " · ") +
        Math.max(1, Math.ceil((new Date(k.expires_at) - Date.now()) / 60000)) +
        tr(" 分", " min left");
    const n = chip(
      text,
      s === "active" ? "success" : s === "grace" ? "warning" : "",
    );
    if (k.expires_at && !k.revoked_at) {
      n.dataset.expires = k.expires_at;
    }
    return n;
  }
  let listTable, search, stateFilter;
  function renderListRows() {
    if (!listTable) return;
    listTable.body.replaceChildren();
    const q = search.value.trim().toLocaleLowerCase();
    const shown = agents.filter(
      (a) =>
        (!stateFilter.value || a.status === stateFilter.value) &&
        (!q ||
          (a.name + " " + (a.keys || []).map((k) => k.prefix).join(" "))
            .toLocaleLowerCase()
            .includes(q)),
    );
    $("agentListCount").textContent = tr("共 ", "Total ") + shown.length;
    shown.forEach((a) => {
      const meta = node("div"),
        aLink = link(a.name, "/admin/agents/" + encodeURIComponent(a.id));
      aLink.className = "agent-name";
      meta.append(
        aLink,
        node("p", a.description, "agent-muted"),
        node("p", owner(a), "agent-muted"),
      );
      const perms = node("div", undefined, "agent-chips");
      (a.permissions || []).forEach((p) =>
        perms.append(node("code", p, "badge")),
      );
      perms.append(node("p", effective(a), "agent-muted"));
      const keys = node("div");
      (a.keys || [])
        .filter((k) => ["active", "grace"].includes(keyState(k)))
        .forEach((k) => {
          const d = node("div");
          d.append(node("code", k.prefix + "…"), keyBadge(k));
          keys.append(d);
        });
      keys.append(
        node(
          "p",
          tr("最後使用：", "Last used: ") + date(a.last_used_at),
          "agent-muted",
        ),
      );
      row(listTable, [
        meta,
        perms,
        keys,
        chip(
          labels[a.status] || a.status,
          a.status === "active" ? "success" : "",
        ),
        link(
          tr("詳情", "Details"),
          "/admin/agents/" + encodeURIComponent(a.id),
        ),
      ]);
    });
    if (!shown.length)
      row(listTable, [
        tr("沒有符合條件的 agent。", "No matching agents."),
        "",
        "",
        "",
        "",
      ]);
  }
  function infoCard() {
    const card = node("section", undefined, "agent-card");
    card.append(
      node("h2", tr("給 agent 的連線資訊", "Connection information")),
    );
    [
      "GET /api/agent/v1",
      "GET /api/agent/v1/openapi.json",
      "Authorization: Bearer jla_…",
      "skills/jelly-cms/SKILL.md",
    ].forEach((x) => card.append(node("p", x, "agent-muted")));
    card.append(
      button(tr("複製連線資訊", "Copy connection information"), async () => {
        try {
          await navigator.clipboard.writeText(
            "API: " +
              location.origin +
              "/api/agent/v1\nOpenAPI: " +
              location.origin +
              "/api/agent/v1/openapi.json\nAuthorization: Bearer jla_…\nSkill: skills/jelly-cms/SKILL.md",
          );
          message(
            status,
            tr("已複製連線資訊。", "Connection information copied."),
          );
        } catch (_) {
          message(
            status,
            tr("無法使用剪貼簿。", "Clipboard unavailable."),
            true,
          );
        }
      }),
    );
    return card;
  }
  function renderList() {
    content.replaceChildren();
    const layout = node("div", undefined, "agent-layout"),
      main = node("section", undefined, "agent-main"),
      side = node("aside", undefined, "agent-side");
    main.append(
      node(
        "div",
        tr(
          "AI agent 以獨立金鑰呼叫 API。有效權限由這裡的設定與擁有者目前角色共同決定。完整金鑰只在建立與輪替時顯示一次。",
          "Agents use their own API keys. Effective permissions depend on the selected permissions and the owner’s current role. Full keys appear only once after creation or rotation.",
        ),
        "agent-note",
      ),
    );
    const filters = node("div", undefined, "agent-filters");
    const sf = field(
      tr("搜尋名稱或金鑰前綴", "Search name or key prefix"),
      "search",
    );
    search = sf.input;
    search.type = "search";
    stateFilter = node("select");
    stateFilter.setAttribute("aria-label", tr("狀態", "Status"));
    [
      ["", tr("全部狀態", "All statuses")],
      ["active", labels.active],
      ["disabled", labels.disabled],
    ].forEach(([v, l]) => stateFilter.append(new Option(l, v)));
    const count = node("span", "", "agent-count");
    count.id = "agentListCount";
    filters.append(sf.wrap, stateFilter, count);
    main.append(filters);
    listTable = table([
      tr("Agent 與擁有者", "Agent and owner"),
      tr("權限", "Permissions"),
      tr("金鑰與最後使用", "Keys and last used"),
      tr("狀態", "Status"),
      "",
    ]);
    main.append(listTable.wrap);
    const create = node("section", undefined, "agent-card agent-create");
    create.id = "agentCreate";
    create.append(
      node("h2", tr("建立 agent", "Create agent")),
      editor(null, true),
      node(
        "p",
        tr(
          "擁有者是目前登入的管理員。需管理員的權限會隨擁有者角色變更而收斂。",
          "The signed-in administrator is the owner. Admin-only permissions stop taking effect if the owner’s role changes.",
        ),
        "agent-muted",
      ),
    );
    side.append(create, infoCard());
    layout.append(main, side);
    content.append(layout);
    search.addEventListener("input", renderListRows);
    stateFilter.addEventListener("change", renderListRows);
    renderListRows();
  }
  let detailSummary, keysTable, activity, history;
  function refreshDetailSummary() {
    if (!current) return;
    detailSummary.replaceChildren(
      node("h2", current.name),
      node("p", current.description, "agent-muted"),
      node("p", owner(current), "agent-muted"),
      chip(
        labels[current.status] || current.status,
        current.status === "active" ? "success" : "",
      ),
      node("p", effective(current), "agent-muted"),
      node(
        "p",
        tr("建立：", "Created: ") +
          date(current.created_at) +
          tr(" · 最後使用：", " · Last used: ") +
          date(current.last_used_at),
        "agent-muted",
      ),
    );
    const toggle = $("agentToggle");
    if (toggle)
      toggle.textContent =
        current.status === "active"
          ? tr("停用", "Disable")
          : tr("啟用", "Enable");
    renderKeys();
  }
  function renderKeys() {
    keysTable.body.replaceChildren();
    (current.keys || []).forEach((k) => {
      const actions = node("div", undefined, "agent-actions");
      if (["active", "grace"].includes(keyState(k)))
        actions.append(
          mutationButton(
            keyState(k) === "grace"
              ? tr("提前撤銷", "Revoke early")
              : tr("撤銷", "Revoke"),
            () => confirmAction("revoke", k),
          ),
        );
      row(keysTable, [
        node("code", k.prefix + "…"),
        keyBadge(k),
        date(k.created_at),
        date(k.last_used_at),
        date(k.expires_at),
        actions,
      ]);
    });
  }
  async function confirmAction(action, key) {
    const target = current;
    const targetEndpoint = endpoint;
    const targetKey = key ? { id:key.id, prefix:key.prefix } : null;
    if (!target || busy || confirmationPending) return;
    confirmationPending = true;
    try {
      const title =
        action === "delete"
          ? tr("刪除 agent", "Delete agent")
          : action === "revoke"
            ? tr("撤銷金鑰", "Revoke key")
            : action === "disable"
              ? tr("停用 agent", "Disable agent")
              : tr("啟用 agent", "Enable agent");
      const body = node("div");
      body.append(
        node(
          "p",
          action === "delete"
            ? tr(
                "所有金鑰會立即失效。活動紀錄與輪替歷史會保留。",
                "All keys will stop working immediately. Activity and rotation history are retained.",
              )
            : action === "revoke"
              ? tr(
                  "撤銷後，這把金鑰的下一次請求會被拒絕。",
                  "This key’s next request will be rejected after revocation.",
                )
              : action === "disable"
                ? tr(
                    "所有金鑰（含過渡中的）會暫停使用，可再啟用。",
                    "All keys, including grace-period keys, will stop working until enabled again.",
                  )
                : tr(
                    "未撤銷且未過期的金鑰將恢復使用。",
                    "Unrevoked, unexpired keys will work again.",
                  ),
        ),
        node("p", targetKey ? targetKey.prefix + "…" : target.name),
      );
      const targetName = target.name;
      const confirmed = await Jelly.confirm({
        title,
        message:body.textContent,
        confirmLabel:tr("確認", "Confirm"),
        cancelLabel:tr("取消", "Cancel"),
        inputLabel:action === "delete" ? tr("輸入「" + targetName + "」確認", "Type \"" + targetName + "\" to confirm") : tr("輸入名稱確認", "Type the name to confirm"),
        tone:action === "delete" || action === "revoke" ? "danger" : "primary",
        ...(action === "delete" ? {requireText:targetName} : {}),
      });
      if (!confirmed || !pageActive || busy) return;
      let path = targetEndpoint, method = "POST";
      if (action === "delete") method = "DELETE";
      else if (action === "revoke") path += "/keys/" + encodeURIComponent(targetKey.id) + "/revoke";
      else path += "/" + action;
      await mutate(path, method, undefined, async (result) => {
        if (action === "delete") {
          location.assign("/admin/agents");
          return;
        }
        if (action === "revoke")
          current.keys = current.keys.map((k) =>
            String(k.id) === String(result.id) ? result : k,
          );
        else current = result;
        refreshDetailSummary();
        activity.reset();
        history.reset();
        message(status, tr("已完成。", "Done."));
      }, status);
    } finally {
      confirmationPending = false;
    }
  }
  function rotate() {
    const form = node("form", undefined, "agent-form");
    const mode = node("fieldset");
    mode.append(node("legend", tr("輪替方式", "Rotation mode")));
    ["immediate", "grace"].forEach((value) => {
      const label = node("label", undefined, "agent-option"),
        radio = node("input");
      radio.type = "radio";
      radio.name = "rotation-mode";
      radio.value = value;
      radio.checked = value === "immediate";
      const text = node(
        "span",
        value === "immediate"
          ? tr("立即失效（預設）", "Revoke immediately (default)")
          : tr("過渡期", "Grace period"),
      );
      text.append(
        node(
          "small",
          value === "immediate"
            ? tr(
                "舊金鑰下一次請求即失效。",
                "The previous key stops working on its next request.",
              )
            : tr(
                "新舊金鑰同時可用，最長 24 小時，可提前撤銷。",
                "Both keys work for up to 24 hours. You can revoke the previous key early.",
              ),
        ),
      );
      label.append(radio, text);
      mode.append(label);
    });
    const f = field(
      tr("過渡分鐘", "Grace period in minutes"),
      "grace-minutes",
      "30",
    );
    f.input.type = "number";
    f.input.min = "1";
    f.input.max = "1440";
    f.input.step = "1";
    const error = node("p", "", "agent-error");
    error.id = "agentGraceError";
    f.input.setAttribute("aria-describedby", error.id);
    const preview = node("p", "", "agent-muted");
    const presets = node("div", undefined, "agent-actions");
    [15, 30, 60, 1440].forEach((n) =>
      presets.append(
        button(String(n), () => {
          f.input.value = n;
          validate();
        }),
      ),
    );
    form.append(
      mode,
      f.wrap,
      presets,
      error,
      preview,
      node(
        "p",
        tr(
          "ID、權限、擁有者與紀錄不變。更早的過渡金鑰會立即撤銷。",
          "ID, permissions, owner and history stay unchanged. Any older grace-period key is revoked immediately.",
        ),
        "agent-muted",
      ),
    );
    const confirm = mutationButton(
      tr("輪替金鑰", "Rotate key"),
      () => {
        if (!validate()) return;
        const value = form.querySelector("[name=rotation-mode]:checked").value;
        mutate(
          endpoint + "/rotate",
          "POST",
          {
            mode: value,
            ...(value === "grace"
              ? { grace_minutes: Number(f.input.value) }
              : {}),
          },
          async (result) => {
            current = result.agent;
            refreshDetailSummary();
            showSecret(result);
            activity.reset();
            history.reset();
          },
        );
      },
      "primary",
    );
    function validate() {
      const grace =
          form.querySelector("[name=rotation-mode]:checked").value === "grace",
        num = Number(f.input.value),
        valid =
          !grace ||
          (f.input.value.trim() !== "" &&
            Number.isInteger(num) &&
            num >= 1 &&
            num <= 1440);
      f.wrap.hidden = !grace;
      presets.hidden = !grace;
      error.textContent = valid ? "" : graceError;
      f.input.setAttribute("aria-invalid", String(!valid));
      confirm.dataset.invalid = String(!valid);
      confirm.disabled = busy || !valid;
      preview.textContent =
        grace && valid
          ? tr("舊金鑰預計到期：", "Previous key will expire around: ") +
            date(new Date(Date.now() + num * 60000).toISOString())
          : "";
      return valid;
    }
    form.addEventListener("input", validate);
    form.addEventListener("change", validate);
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (!confirm.disabled) confirm.click();
    });
    openDialog(tr("輪替金鑰", "Rotate key"), form, [confirm, cancel()]);
    validate();
  }
  function eventSection(title, isHistory) {
    const section = node("section", undefined, "agent-card");
    section.append(node("h2", title));
    const filters = node("form", undefined, "agent-filters"),
      controls = {};
    function select(name, label, values) {
      const wrap = node("label", label),
        s = node("select");
      s.name = name;
      s.setAttribute("aria-label", label);
      values.forEach(([v, l]) => s.append(new Option(l, v)));
      wrap.append(s);
      filters.append(wrap);
      controls[name] = s;
    }
    if (!isHistory) {
      select("status", tr("狀態", "Status"), [
        ["", tr("全部", "All")],
        ...["2xx", "3xx", "4xx", "5xx"].map((v) => [v, v]),
      ]);
      select("module", tr("模組", "Module"), [
        ["", tr("全部模組", "All modules")],
        ...Object.entries({
          ...resources,
          me: "me",
          agents: tr("管理操作", "Management"),
        }),
      ]);
      select("key_id", tr("金鑰", "Key"), [
        ["", tr("所有金鑰", "All keys")],
        ...(current.keys || []).map((k) => [String(k.id), k.prefix + "…"]),
      ]);
      ["since", "until"].forEach((name) => {
        const f = field(
          name === "since"
            ? tr("開始時間", "From")
            : tr("結束時間（不含）", "Until (exclusive)"),
          name,
        );
        f.input.type = "datetime-local";
        controls[name] = f.input;
        filters.append(f.wrap);
      });
      const apply = button(tr("套用篩選", "Apply filters"), () => reset());
      filters.append(apply);
      filters.addEventListener("submit", (e) => {
        e.preventDefault();
        reset();
      });
      section.append(filters);
    }
    const total = node("p", "", "agent-muted"),
      msg = node("p", ""),
      retry = button(tr("重試", "Retry"), () => load()),
      t = table(
        isHistory
          ? [tr("時間", "Time"), tr("操作", "Action"), tr("摘要", "Summary")]
          : [
              tr("時間", "Time"),
              tr("金鑰", "Key"),
              tr("請求", "Request"),
              tr("狀態", "Status"),
              tr("來源", "Source"),
            ],
      );
    msg.setAttribute("role", "status");
    retry.hidden = true;
    const more = button(tr("載入更多", "Load more"), () => load());
    section.append(total, msg, retry, t.wrap, more);
    let cursor = null,
      loaded = 0,
      generation = 0,
      loading = false,
      started = false,
      appliedParams = null;
    function filterParams() {
      const p = new URLSearchParams({ agent_id: id, limit: "50" });
      if (isHistory) {
        p.set("module", "agents");
        p.append("action", "key.rotate");
        p.append("action", "key.revoke");
      } else
        Object.entries(controls).forEach(([k, v]) => {
          if (v.value)
            p.set(
              k,
              k === "since" || k === "until"
                ? new Date(v.value).toISOString()
                : v.value,
            );
        });
      return p;
    }
    async function load() {
      if (loading) return;
      const mine = generation;
      loading = true;
      more.disabled = true;
      retry.hidden = true;
      message(msg, tr("載入中…", "Loading…"));
      try {
        const p = new URLSearchParams(appliedParams);
        if (cursor) p.set("cursor", cursor);
        const result = await api("/api/admin/agents/events?" + p);
        if (mine !== generation) return;
        const items = result.items;
        items.forEach((e) => {
          const summary = e.summary || {};
          if (isHistory) {
            const detail =
              e.action === "key.rotate"
                ? (summary.mode === "grace"
                    ? tr("過渡期 ", "Grace period ") +
                      summary.grace_minutes +
                      tr(" 分", " min")
                    : tr("立即失效", "Immediate revocation")) +
                  " · " +
                  (summary.old_key_prefix || "—") +
                  " → " +
                  (summary.new_key_prefix || e.key_prefix || "—")
                : e.key_prefix || "—";
            row(t, [date(e.at), e.action, detail]);
          } else {
            const request = node("div");
            request.append(
              node("code", (e.method || "") + " " + (e.route || e.action)),
              node("p", JSON.stringify(summary), "agent-muted"),
            );
            row(t, [
              date(e.at),
              node("code", e.key_prefix ? e.key_prefix + "…" : "—"),
              request,
              chip(
                Number(e.status) === 0
                  ? tr("結果尚未確認", "Result unconfirmed")
                  : String(e.status),
                Number(e.status) === 0
                  ? ""
                  : Number(e.status) >= 400
                    ? "warning"
                    : "success",
              ),
              node("code", e.ip_masked || "—"),
            ]);
          }
        });
        loaded += items.length;
        cursor = result.next_cursor || null;
        started = true;
        more.hidden = !cursor;
        total.textContent =
          tr("共 ", "Total ") +
          result.total +
          tr(" 筆 · 已載入 ", " · Loaded ") +
          loaded;
        message(msg, loaded ? "" : tr("沒有紀錄。", "No events."));
      } catch (e) {
        if (mine === generation) {
          message(msg, e.message, true);
          retry.hidden = false;
        }
      } finally {
        if (mine === generation) {
          loading = false;
          more.disabled = false;
        }
      }
    }
    function reset() {
      if (controls.key_id) {
        const selected = controls.key_id.value;
        controls.key_id.replaceChildren(
          new Option(tr("所有金鑰", "All keys"), ""),
          ...(current.keys || []).map(
            (k) => new Option(k.prefix + "…", String(k.id)),
          ),
        );
        if ([...controls.key_id.options].some((o) => o.value === selected))
          controls.key_id.value = selected;
      }
      let nextParams;
      try {
        nextParams = filterParams();
      } catch (_) {
        message(
          msg,
          tr("請輸入有效的篩選時間。", "Enter a valid filter date and time."),
          true,
        );
        return;
      }
      appliedParams = nextParams;
      generation++;
      loading = false;
      cursor = null;
      loaded = 0;
      started = false;
      t.body.replaceChildren();
      total.textContent = "";
      more.hidden = true;
      load();
    }
    return { section, reset };
  }
  function renderDetail() {
    content.replaceChildren();
    const back = link(
      tr("← AI agent 清單", "← All AI agents"),
      "/admin/agents",
    );
    content.append(back);
    const actions = node("div", undefined, "agent-actions");
    actions.append(
      mutationButton(tr("輪替金鑰", "Rotate key"), rotate, "primary"),
    );
    const toggle = mutationButton(
      current.status === "active"
        ? tr("停用", "Disable")
        : tr("啟用", "Enable"),
      () => confirmAction(current.status === "active" ? "disable" : "enable"),
    );
    toggle.id = "agentToggle";
    actions.append(
      toggle,
      mutationButton(
        tr("刪除", "Delete"),
        () => confirmAction("delete"),
        "danger",
      ),
    );
    content.append(actions);
    const layout = node("div", undefined, "agent-layout"),
      main = node("section", undefined, "agent-main"),
      side = node("aside", undefined, "agent-side");
    detailSummary = node("section", undefined, "agent-card");
    main.append(detailSummary);
    const keyCard = node("section", undefined, "agent-card");
    keyCard.append(node("h2", tr("金鑰", "Keys")));
    keysTable = table([
      tr("前綴", "Prefix"),
      tr("狀態", "Status"),
      tr("建立時間", "Created"),
      tr("最後使用", "Last used"),
      tr("到期時間", "Expires"),
      "",
    ]);
    keysTable.wrap.classList.add("agent-key-table-wrap");
    keysTable.wrap.tabIndex = 0;
    keysTable.wrap.setAttribute("role", "region");
    keysTable.wrap.setAttribute("aria-label", tr("金鑰", "Keys"));
    keysTable.wrap.setAttribute("aria-describedby", "agentKeyScrollHint");
    const scrollHint = node(
      "p",
      tr(
        "可左右捲動查看完整金鑰資料與操作。",
        "Scroll horizontally to view all key details and actions.",
      ),
      "agent-muted agent-scroll-hint",
    );
    scrollHint.id = "agentKeyScrollHint";
    keyCard.append(scrollHint, keysTable.wrap);
    main.append(keyCard);
    history = eventSection(tr("輪替歷史", "Rotation history"), true);
    activity = eventSection(tr("活動紀錄", "Activity"), false);
    const pair = node("div", undefined, "agent-pair");
    const edit = node("section", undefined, "agent-card");
    edit.append(
      node("h2", tr("基本資料與權限", "Details and permissions")),
      editor(current, false),
      node(
        "p",
        tr(
          "有效權限由擁有者目前角色決定。",
          "Effective permissions depend on the owner’s current role.",
        ),
        "agent-muted",
      ),
    );
    pair.append(edit, history.section);
    main.append(pair, activity.section);
    side.append(infoCard());
    layout.append(main, side);
    content.append(layout);
    refreshDetailSummary();
    history.reset();
    activity.reset();
  }
  async function init(preserveInput = false) {
    const mine = ++epoch;
    setBusy(true);
    root.setAttribute("aria-busy", "true");
    $("agentsRetry").hidden = true;
    message(status, tr("載入中…", "Loading…"));
    try {
      const [discovery, data] = await Promise.all([
        api("/api/agent/v1"),
        api(endpoint),
      ]);
      if (mine !== epoch || !pageActive) return;
      permissions = discovery.permissions || [];
      if (!permissions.length)
        throw new Error(tr("無法取得權限清單。", "Cannot load permissions."));
      if (id) {
        current = data;
        if (preserveInput && detailSummary) {
          refreshDetailSummary();
          history.reset();
          activity.reset();
        } else renderDetail();
      } else {
        agents = data.items || [];
        if (preserveInput && listTable) renderListRows();
        else renderList();
      }
      message(status, "");
    } catch (e) {
      if (mine === epoch && pageActive) {
        message(status, e.message, true);
        $("agentsRetry").hidden = false;
      }
    } finally {
      if (mine === epoch && pageActive) {
        root.setAttribute("aria-busy", "false");
        setBusy(false);
      }
    }
  }
  $("agentsRetry").addEventListener("click", () => init(true));
  $("agentDialogClose").addEventListener("click", () => closeDialog());
  dialog.addEventListener("cancel", (e) => {
    e.preventDefault();
    closeDialog();
  });
  dialog.addEventListener("close", () => {
    purgeSecret();
    $("agentDialogBody").replaceChildren();
    $("agentDialogActions").replaceChildren();
  });
  dialog.addEventListener("keydown", (e) => {
    if (e.key !== "Tab") return;
    const all = [
      ...dialog.querySelectorAll(
        "button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),a[href]",
      ),
    ].filter((n) => n.getClientRects().length);
    if (!all.length) {
      e.preventDefault();
      return;
    }
    const first = all[0],
      last = all[all.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  });
  const countdown = setInterval(() => {
    root.querySelectorAll("[data-expires]").forEach((n) => {
      const minutes = Math.ceil(
        (new Date(n.dataset.expires) - Date.now()) / 60000,
      );
      n.textContent =
        minutes <= 0
          ? labels.expired
          : labels.grace +
            tr(" · 剩 ", " · ") +
            minutes +
            tr(" 分", " min left");
    });
  }, 1000);
  function leave() {
    pageActive = false;
    epoch++;
    setBusy(false);
    purgeSecret();
    if (dialog.open) closeDialog(true);
    controllers.forEach((c) => c.abort());
  }
  window.addEventListener("pagehide", leave);
  window.addEventListener("beforeunload", purgeSecret);
  window.addEventListener("pageshow", (e) => {
    pageActive = true;
    if (e.persisted) {
      purgeSecret();
      init(true);
    }
  });
  init();
})();
