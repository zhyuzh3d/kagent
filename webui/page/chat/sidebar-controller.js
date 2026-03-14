export function createSidebarController(options) {
  const {
    app,
    el,
    chatStore,
    sessionController,
    appendDebug,
  } = options;

  let projects = [];
  let currentProjectId = "";
  let currentThreadId = "";

  async function init() {
    appendDebug('INFO', 'Sidebar', null, null, 'Initializing sidebar...');
    await fetchProjects();
    appendDebug('INFO', 'Sidebar', null, null, `Found ${projects.length} projects`);
    // Auto-select or create if none
    if (projects.length === 0) {
      appendDebug('INFO', 'Sidebar', null, null, 'No projects found, creating default...');
      await createDefaultProject();
    } else {
      // Find default or first
      currentProjectId = projects[0].project_id;
      appendDebug('INFO', 'Sidebar', null, null, `Selecting first project: ${currentProjectId}`);
      const threads = await fetchThreads(currentProjectId);
      appendDebug('INFO', 'Sidebar', null, null, `Found ${threads.length} threads for project`);
      if (threads.length === 0) {
        currentThreadId = await createDefaultThread(currentProjectId);
      } else {
        currentThreadId = threads[0].thread_id;
      }
    }
    appendDebug('INFO', 'Sidebar', null, null, `Final context: proj=${currentProjectId} thd=${currentThreadId}`);
    await render();
    syncHeader();
    appendDebug('INFO', 'Sidebar', null, null, 'Sidebar initialization complete');
  }

  async function fetchProjects() {
    try {
      const resp = await fetch('/api/projects');
      if (!resp.ok) throw new Error('fetch projects failed');
      projects = (await resp.json()) || [];
    } catch (err) {
      appendDebug('ERROR', 'Sidebar', null, null, `fetch projects failed: ${err.message}`);
      projects = [];
    }
  }

  async function fetchThreads(projectId) {
    try {
      const resp = await fetch(`/api/projects/${projectId}/threads`);
      if (!resp.ok) throw new Error('fetch threads failed');
      const data = await resp.json();
      return Array.isArray(data) ? data : [];
    } catch (err) {
      appendDebug('ERROR', 'Sidebar', null, null, `fetch threads failed: ${err.message}`);
      return [];
    }
  }

  async function createDefaultProject() {
    try {
      const resp = await fetch('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Default Project' })
      });
      const data = await resp.json();
      currentProjectId = data.project_id;
      await fetchProjects();
      currentThreadId = await createDefaultThread(currentProjectId);
    } catch (err) {
      appendDebug('ERROR', 'Sidebar', null, null, `create default project failed: ${err.message}`);
    }
  }

  async function createDefaultThread(projectId) {
    try {
      const resp = await fetch(`/api/projects/${projectId}/threads`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Default Thread' })
      });
      const data = await resp.json();
      return data.thread_id;
    } catch (err) {
      appendDebug('ERROR', 'Sidebar', null, null, `create default thread failed: ${err.message}`);
      return "";
    }
  }

  function syncHeader() {
    const p = projects.find(x => x.project_id === currentProjectId);
    if (p) el.currentProjectName.textContent = p.title;
    // We need to find the thread title. Ideally we cache thread lists per project.
    // For now, let's just set it to "..." and update it later if needed.
    // Or we keep a map of thread titles.
  }

  async function switchThread(projectId, threadId, threadTitle) {
    if (currentThreadId === threadId) return;

    const wasRunning = app.running;
    if (wasRunning) {
      appendDebug('INFO', 'Sidebar', null, null, `Switching thread while running. Stopping current session...`);
      sessionController.stopAll('切换线程');
    }

    currentProjectId = projectId;
    currentThreadId = threadId;
    el.currentThreadName.textContent = threadTitle || "Default Thread";
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
    render();
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
      const resp = await fetch('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title })
      });
      if (resp.ok) {
        await fetchProjects();
        render();
      }
    } catch (err) {
      alert("创建失败: " + err.message);
    }
  }

  async function handleAddThread(projectId) {
    const title = prompt("请输入会话名称", "新会话");
    if (!title) return;
    try {
      const resp = await fetch(`/api/projects/${projectId}/threads`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title })
      });
      if (resp.ok) {
        const data = await resp.json();
        await render(); // Refresh list
        switchThread(projectId, data.thread_id, title);
      }
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
      const resp = await fetch(`/api/projects/${projectId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, order_index: p.order_index })
      });
      if (resp.ok) {
        await fetchProjects();
        render();
        if (currentProjectId === projectId) syncHeader();
      }
    } catch (err) {
      alert("修改失败: " + err.message);
    }
  }

  async function handleDeleteProject(projectId) {
    if (!confirm("确定删除该项目及其所有会话吗？此操作不可撤销。")) return;
    try {
      const resp = await fetch(`/api/projects/${projectId}`, { method: 'DELETE' });
      if (resp.ok) {
        await fetchProjects();
        if (currentProjectId === projectId) {
          // If we deleted current project, try to find another one
          if (projects.length > 0) {
            const firstProj = projects[0];
            const threads = await fetchThreads(firstProj.project_id);
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
        render();
      }
    } catch (err) {
      alert("删除失败: " + err.message);
    }
  }

  async function handleEditThread(threadId) {
    const title = prompt("修改会话名称");
    if (!title) return;
    try {
      const resp = await fetch(`/api/threads/${threadId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, project_id: currentProjectId })
      });
      if (resp.ok) {
        if (currentThreadId === threadId) {
          el.currentThreadName.textContent = title;
        }
        render();
      }
    } catch (err) {
      alert("修改失败: " + err.message);
    }
  }

  async function handleDeleteThread(threadId) {
    if (!confirm("确定删除该会话吗？")) return;
    try {
      const resp = await fetch(`/api/threads/${threadId}`, { method: 'DELETE' });
      if (resp.ok) {
        if (currentThreadId === threadId) {
          // Find another thread in same project
          const threads = await fetchThreads(currentProjectId);
          if (threads.length > 0) {
            await switchThread(currentProjectId, threads[0].thread_id, threads[0].title);
          } else {
            const tid = await createDefaultThread(currentProjectId);
            await switchThread(currentProjectId, tid, "Default Thread");
          }
        }
        render();
      }
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
          const resp = await fetch(`/api/threads/${draggedId}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ project_id: destProjId, order_index: newOrderIndex })
          });
          if (resp.ok) {
            if (currentThreadId === draggedId) currentProjectId = destProjId;
            render();
          }
        } catch (err) {
          appendDebug('ERROR', 'Sidebar', null, null, `drop thread failed: ${err.message}`);
        }
      } else if (draggedType === "project" && targetProjItem) {
        const destProjId = targetProjItem.dataset.projectId;
        if (destProjId === draggedId) return;

        const targetProj = projects.find(p => p.project_id === destProjId);
        if (!targetProj) return;

        try {
          const resp = await fetch(`/api/projects/${draggedId}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ order_index: targetProj.order_index })
          });
          if (resp.ok) {
            await fetchProjects();
            render();
          }
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
