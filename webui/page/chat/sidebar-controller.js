import { callTool } from "./tool-call.js";

export function createSidebarController(options) {
  const {
    app,
    el,
    chatStore,
    sessionController,
    appendDebug,
  } = options;

  let projects = [];
  const threadCache = new Map();
  let currentProjectId = "";
  let currentThreadId = "";
  let currentThreadTitle = "";
  const contextStorageKey = "kagent:chat:sidebar:context";

  function readStoredContext() {
    try {
      const raw = window.localStorage.getItem(contextStorageKey);
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      return parsed && typeof parsed === "object" ? parsed : null;
    } catch (_) {
      return null;
    }
  }

  function persistCurrentContext() {
    try {
      if (!currentProjectId || !currentThreadId) return;
      window.localStorage.setItem(contextStorageKey, JSON.stringify({
        project_id: currentProjectId,
        thread_id: currentThreadId,
      }));
    } catch (_) {
    }
  }

  function pickMostRecentProject(list) {
    if (!Array.isArray(list) || list.length === 0) return null;
    return list.reduce((best, item) => {
      if (!best) return item;
      const bestScore = Number(best.last_active_at_ms) || Number(best.created_at_ms) || 0;
      const itemScore = Number(item.last_active_at_ms) || Number(item.created_at_ms) || 0;
      if (itemScore > bestScore) return item;
      return best;
    }, null);
  }

  function pickMostRecentThread(list) {
    if (!Array.isArray(list) || list.length === 0) return null;
    return list.reduce((best, item) => {
      if (!best) return item;
      const bestScore = Number(best.last_active_at_ms) || Number(best.created_at_ms) || 0;
      const itemScore = Number(item.last_active_at_ms) || Number(item.created_at_ms) || 0;
      if (itemScore > bestScore) return item;
      return best;
    }, null);
  }

  async function resolveInitialContext() {
    if (projects.length === 0) {
      appendDebug('INFO', 'Sidebar', null, null, 'No projects found, creating default...');
      await createDefaultProject();
      currentThreadTitle = "Default Thread";
      persistCurrentContext();
      return;
    }

    const stored = readStoredContext();
    const savedProjectId = stored && typeof stored.project_id === "string" ? stored.project_id : "";
    const savedThreadId = stored && typeof stored.thread_id === "string" ? stored.thread_id : "";
    let project = projects.find((item) => item.project_id === savedProjectId) || pickMostRecentProject(projects) || projects[0];
    currentProjectId = project && project.project_id ? project.project_id : "";
    appendDebug('INFO', 'Sidebar', null, null, `Selecting project: ${currentProjectId}${savedProjectId ? ` (saved=${savedProjectId})` : ''}`);

    let threads = currentProjectId ? await fetchThreads(currentProjectId, { force: true }) : [];
    let thread = threads.find((item) => item.thread_id === savedThreadId) || pickMostRecentThread(threads) || threads[0];
    if (!thread && currentProjectId) {
      const createdThreadId = await createDefaultThread(currentProjectId);
      threads = await fetchThreads(currentProjectId, { force: true });
      thread = threads.find((item) => item.thread_id === createdThreadId) || threads[0] || null;
    }

    currentThreadId = thread && thread.thread_id ? thread.thread_id : "";
    currentThreadTitle = thread && thread.title ? thread.title : "Default Thread";
    persistCurrentContext();
  }

  async function init() {
    appendDebug('INFO', 'Sidebar', null, null, 'Initializing sidebar...');
    try {
      await fetchProjects();
      appendDebug('INFO', 'Sidebar', null, null, `Found ${projects.length} projects`);
      await resolveInitialContext();
      appendDebug('INFO', 'Sidebar', null, null, `Final context: proj=${currentProjectId} thd=${currentThreadId}`);
      await render();
      syncHeader();
      appendDebug('INFO', 'Sidebar', null, null, 'Sidebar initialization complete');
    } catch (err) {
      appendDebug('ERROR', 'Sidebar', null, null, `sidebar init failed: ${err.message}`);
      throw err;
    }
  }

  async function fetchProjects() {
    const result = await callTool("app.chat.project_list");
    projects = Array.isArray(result && result.items) ? result.items : [];
  }

  async function fetchThreads(projectId, options = {}) {
    const force = !!(options && options.force);
    if (!force && threadCache.has(projectId)) {
      return threadCache.get(projectId) || [];
    }
    const result = await callTool("app.chat.thread_list", { project_id: projectId });
    const items = Array.isArray(result && result.items) ? result.items : [];
    threadCache.set(projectId, items);
    return items;
  }

  function invalidateThreads(projectId) {
    if (!projectId) return;
    threadCache.delete(projectId);
  }

  async function createDefaultProject() {
    const result = await callTool("app.chat.project_create", { title: "Default Project" });
    currentProjectId = (result && result.project_id) || "";
    await fetchProjects();
    currentThreadId = await createDefaultThread(currentProjectId);
    currentThreadTitle = "Default Thread";
  }

  async function createDefaultThread(projectId) {
    const result = await callTool("app.chat.thread_create", {
      project_id: projectId,
      title: "Default Thread",
    });
    return (result && result.thread_id) || "";
  }

  function syncHeader() {
    const p = projects.find(x => x.project_id === currentProjectId);
    if (p) el.currentProjectName.textContent = p.title;
    el.currentThreadName.textContent = currentThreadTitle || "Default Thread";
  }

  async function switchThread(projectId, threadId, threadTitle) {
    if (currentProjectId === projectId && currentThreadId === threadId) return;

    const wasRunning = app.running;
    if (wasRunning) {
      appendDebug('INFO', 'Sidebar', null, null, `Switching thread while running. Stopping current session...`);
      sessionController.stopAll('切换线程');
    }

    currentProjectId = projectId;
    currentThreadId = threadId;
    currentThreadTitle = threadTitle || "Default Thread";
    persistCurrentContext();
    syncHeader();
    
    chatStore.clearForJump();
    app.activeTurnId = 0;
    app.currentTurn = 0;
    
    if (wasRunning) {
      appendDebug('INFO', 'Sidebar', null, null, `Restarting session in new thread: ${threadId}`);
      await sessionController.startAll(projectId, threadId);
    } else {
      await sessionController.reconnectWith(projectId, threadId);
    }
    await render();
  }

  async function render() {
    appendDebug('INFO', 'Sidebar', null, null, 'Rendering sidebar UI...');
    const sidebar = el.sidebar;
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <h2>项目列表</h2>
        <button id="addProjectBtn" class="btn-action" title="新建项目">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>
        </button>
      </div>
      <div id="sidebarScroll" class="sidebar-scroll"></div>
      <div class="sidebar-footer">
        <button id="newProjectBtnFooter" class="btn-new-project">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>
          新建项目
        </button>
      </div>
    `;

    const scroll = sidebar.querySelector('#sidebarScroll');
    appendDebug('INFO', 'Sidebar', null, null, `Rendering ${projects.length} projects in loop`);
    
    for (const p of projects) {
      const projEl = document.createElement('div');
      projEl.className = `project-item ${p.project_id === currentProjectId ? 'expanded' : ''}`;
      projEl.dataset.projectId = p.project_id;
      projEl.draggable = true;
      
      projEl.innerHTML = `
        <div class="project-header ${p.project_id === currentProjectId ? 'active' : ''}">
          <div class="project-arrow">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="9 18 15 12 9 6"></polyline></svg>
          </div>
          <div class="project-title">${p.title}</div>
          <div class="item-actions">
            <button class="btn-action btn-add-thread" title="新建会话">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"></line><line x1="5" y1="12" x2="19" y2="12"></line></svg>
            </button>
            <button class="btn-action btn-edit-proj" title="重命名">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
            </button>
            <button class="btn-action btn-del-proj" title="删除">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
            </button>
          </div>
        </div>
        <div class="thread-list"></div>
      `;

      const threadListEl = projEl.querySelector('.thread-list');
      
      // We fetch threads for each project to render them
      // In a real app we might want to lazy load these or cache them
      const pThreads = await fetchThreads(p.project_id);
      for (const t of pThreads) {
        if (t.thread_id === currentThreadId) {
          el.currentThreadName.textContent = t.title;
        }
        const threadEl = document.createElement('div');
        threadEl.className = `thread-item ${t.thread_id === currentThreadId ? 'active' : ''}`;
        threadEl.dataset.threadId = t.thread_id;
        threadEl.dataset.projectId = p.project_id;
        threadEl.draggable = true;
        threadEl.innerHTML = `
          <div class="thread-title">${t.title}</div>
          <div class="item-actions">
            <button class="btn-action btn-edit-thd" title="重命名">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
            </button>
            <button class="btn-action btn-del-thd" title="删除">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
            </button>
          </div>
        `;
        threadEl.addEventListener('click', (e) => {
          e.stopPropagation();
          switchThread(p.project_id, t.thread_id, t.title);
        });
        threadListEl.appendChild(threadEl);
      }

      projEl.querySelector('.project-header').addEventListener('click', () => {
        projEl.classList.toggle('expanded');
      });

      scroll.appendChild(projEl);
    }

    // Add event listeners for new project, etc.
    sidebar.querySelector('#addProjectBtn').onclick = handleAddProject;
    sidebar.querySelector('#newProjectBtnFooter').onclick = handleAddProject;

    // Delegate actions
    sidebar.querySelectorAll('.btn-add-thread').forEach(btn => {
      btn.onclick = (e) => {
        e.stopPropagation();
        handleAddThread(btn.closest('.project-item').dataset.projectId);
      };
    });
    sidebar.querySelectorAll('.btn-edit-proj').forEach(btn => {
      btn.onclick = (e) => {
        e.stopPropagation();
        handleEditProject(btn.closest('.project-item').dataset.projectId);
      };
    });
    sidebar.querySelectorAll('.btn-del-proj').forEach(btn => {
      btn.onclick = (e) => {
        e.stopPropagation();
        handleDeleteProject(btn.closest('.project-item').dataset.projectId);
      };
    });
    sidebar.querySelectorAll('.btn-edit-thd').forEach(btn => {
      btn.onclick = (e) => {
        e.stopPropagation();
        handleEditThread(btn.closest('.thread-item').dataset.threadId);
      };
    });
    sidebar.querySelectorAll('.btn-del-thd').forEach(btn => {
      btn.onclick = (e) => {
        e.stopPropagation();
        handleDeleteThread(btn.closest('.thread-item').dataset.threadId);
      };
    });

    setupDragAndDrop();
  }

  async function handleAddProject() {
    const title = prompt("请输入项目名称", "新项目");
    if (!title) return;
    try {
      await callTool("app.chat.project_create", { title });
      await fetchProjects();
      threadCache.clear();
      await render();
    } catch (err) {
      alert("创建失败: " + err.message);
    }
  }

  async function handleAddThread(projectId) {
    const title = prompt("请输入会话名称", "新会话");
    if (!title) return;
    try {
      const result = await callTool("app.chat.thread_create", {
        project_id: projectId,
        title,
      });
      invalidateThreads(projectId);
      await fetchThreads(projectId, { force: true });
      await render(); // Refresh list
      await switchThread(projectId, (result && result.thread_id) || "", title);
    } catch (err) {
      alert("创建失败: " + err.message);
    }
  }

  async function handleEditProject(projectId) {
    const p = projects.find(x => x.project_id === projectId);
    if (!p) return;
    const title = prompt("修改项目名称", p.title);
    if (!title || title === p.title) return;
    try {
      await callTool("app.chat.project_update", {
        project_id: projectId,
        title,
        order_index: p.order_index,
      });
      await fetchProjects();
      await render();
      if (currentProjectId === projectId) syncHeader();
    } catch (err) {
      alert("修改失败: " + err.message);
    }
  }

  async function handleDeleteProject(projectId) {
    if (!confirm("确定删除该项目及其所有会话吗？此操作不可撤销。")) return;
    try {
      await callTool("app.chat.project_delete", { project_id: projectId });
      await fetchProjects();
      invalidateThreads(projectId);
      if (currentProjectId === projectId) {
        // If we deleted current project, try to find another one
        if (projects.length > 0) {
          const firstProj = pickMostRecentProject(projects) || projects[0];
          const threads = await fetchThreads(firstProj.project_id, { force: true });
          if (threads.length > 0) {
            await switchThread(firstProj.project_id, threads[0].thread_id, threads[0].title);
          } else {
            const tid = await createDefaultThread(firstProj.project_id);
            await switchThread(firstProj.project_id, tid, "Default Thread");
          }
        } else {
          await createDefaultProject();
        }
      }
      await render();
    } catch (err) {
      alert("删除失败: " + err.message);
    }
  }

  async function handleEditThread(threadId) {
    const title = prompt("修改会话名称");
    if (!title) return;
    try {
      await callTool("app.chat.thread_update", {
        thread_id: threadId,
        title,
        project_id: currentProjectId,
      });
      invalidateThreads(currentProjectId);
      await fetchThreads(currentProjectId, { force: true });
      if (currentThreadId === threadId) {
        currentThreadTitle = title;
        persistCurrentContext();
        syncHeader();
      }
      await render();
    } catch (err) {
      alert("修改失败: " + err.message);
    }
  }

  async function handleDeleteThread(threadId) {
    if (!confirm("确定删除该会话吗？")) return;
    try {
      await callTool("app.chat.thread_delete", { thread_id: threadId });
      invalidateThreads(currentProjectId);
      if (currentThreadId === threadId) {
        // Find another thread in same project
        const threads = await fetchThreads(currentProjectId, { force: true });
        if (threads.length > 0) {
          await switchThread(currentProjectId, threads[0].thread_id, threads[0].title);
        } else {
          const tid = await createDefaultThread(currentProjectId);
          await switchThread(currentProjectId, tid, "Default Thread");
        }
      }
      await render();
    } catch (err) {
      alert("删除失败: " + err.message);
    }
  }

  function setupDragAndDrop() {
    const sidebar = el.sidebar;
    let draggedType = ""; // "project" or "thread"
    let draggedId = "";
    let sourceProjId = "";

    sidebar.addEventListener('dragstart', (e) => {
      const projItem = e.target.closest('.project-item');
      const threadItem = e.target.closest('.thread-item');

      if (threadItem) {
        draggedType = "thread";
        draggedId = threadItem.dataset.threadId;
        sourceProjId = threadItem.dataset.projectId;
        threadItem.classList.add('dragging');
        e.dataTransfer.setData('text/plain', draggedId);
      } else if (projItem) {
        draggedType = "project";
        draggedId = projItem.dataset.projectId;
        projItem.classList.add('dragging');
        e.dataTransfer.setData('text/plain', draggedId);
      }
    });

    sidebar.addEventListener('dragend', (e) => {
      sidebar.querySelectorAll('.dragging').forEach(el => el.classList.remove('dragging'));
      sidebar.querySelectorAll('.drop-target').forEach(el => el.classList.remove('drop-target'));
    });

    sidebar.addEventListener('dragover', (e) => {
      e.preventDefault();
      const targetProj = e.target.closest('.project-header');
      const targetThread = e.target.closest('.thread-item');

      sidebar.querySelectorAll('.drop-target').forEach(el => el.classList.remove('drop-target'));

      if (draggedType === "thread") {
        if (targetThread) {
          targetThread.classList.add('drop-target');
        } else if (targetProj) {
          targetProj.classList.add('drop-target'); // Move to this project
        }
      } else if (draggedType === "project" && targetProj) {
        targetProj.closest('.project-item').classList.add('drop-target');
      }
    });

    sidebar.addEventListener('drop', async (e) => {
      e.preventDefault();
      const targetProjHeader = e.target.closest('.project-header');
      const targetProjItem = e.target.closest('.project-item');
      const targetThread = e.target.closest('.thread-item');

      if (draggedType === "thread") {
        const destProjId = targetThread ? targetThread.dataset.projectId : (targetProjHeader ? targetProjHeader.closest('.project-item').dataset.projectId : null);
        if (!destProjId) return;

        // Find target position
        let newOrderIndex = 0;
        if (targetThread) {
          // Put before targetThread
          const threads = await fetchThreads(destProjId);
          const targetIdx = threads.findIndex(t => t.thread_id === targetThread.dataset.threadId);
          newOrderIndex = threads[targetIdx].order_index;
          // We should ideally shift all others, but for now we just use target index
        } else {
          // Put at end of project
          const threads = await fetchThreads(destProjId);
          newOrderIndex = threads.length > 0 ? Math.max(...threads.map(t => t.order_index)) + 1 : 0;
        }

        try {
          await callTool("app.chat.thread_update", {
            thread_id: draggedId,
            project_id: destProjId,
            order_index: newOrderIndex,
          });
          invalidateThreads(sourceProjId);
          invalidateThreads(destProjId);
          if (currentThreadId === draggedId) currentProjectId = destProjId;
          await render();
        } catch (err) {
          appendDebug('ERROR', 'Sidebar', null, null, `drop thread failed: ${err.message}`);
        }
      } else if (draggedType === "project" && targetProjItem) {
        const destProjId = targetProjItem.dataset.projectId;
        if (destProjId === draggedId) return;

        const targetProj = projects.find(p => p.project_id === destProjId);
        if (!targetProj) return;

        try {
          await callTool("app.chat.project_update", {
            project_id: draggedId,
            order_index: targetProj.order_index,
          });
          await fetchProjects();
          await render();
        } catch (err) {
          appendDebug('ERROR', 'Sidebar', null, null, `drop project failed: ${err.message}`);
        }
      }
    });
  }

  return {
    init,
    render,
    getCurrentContext: () => ({ projectId: currentProjectId, threadId: currentThreadId })
  };
}
