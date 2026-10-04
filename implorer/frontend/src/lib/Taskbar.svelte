<script lang="ts">
  let windows: { hwnd: number; title: string }[] = $state([]);
  let time = $state(
    new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
  );
  let menuOpen = $state(false);

  const apps = [
    { name: "PuTTY", path: "E:\\putty.exe" },
    { name: "CMD", path: "C:\\Windows\\System32\\cmd.exe" },
    { name: "Notepad", path: "C:\\Windows\\System32\\notepad.exe" },
  ];

  setInterval(() => {
    time = new Date().toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    });
  }, 10000);

  async function refreshWindows() {
    try {
      // @ts-ignore
      const result = await window.go.main.App.GetWindows();
      if (result) windows = result;
    } catch {}
  }

  setInterval(refreshWindows, 2000);
  refreshWindows();

  async function activate(hwnd: number) {
    try {
      // @ts-ignore
      await window.go.main.App.ActivateWindow(hwnd);
    } catch {}
  }

  async function launch(path: string) {
    try {
      // @ts-ignore
      await window.go.main.App.LaunchApp(path);
      menuOpen = false;
      setTimeout(refreshWindows, 1000);
    } catch {}
  }

  function toggleMenu() {
    menuOpen = !menuOpen;
  }
</script>

<div class="taskbar">
  <div class="start-wrapper">
    <button class="start-btn" onclick={toggleMenu}>
      <span class="start-icon">▦</span>
      <span class="start-label">implorer</span>
    </button>

    {#if menuOpen}
      <div class="start-menu">
        <div class="menu-header">launch</div>
        {#each apps as app}
          <button class="menu-item" onclick={() => launch(app.path)}>
            {app.name}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <div class="divider"></div>

  <div class="window-list">
    {#each windows as win}
      <button class="window-btn" onclick={() => activate(win.hwnd)}>
        {win.title.length > 24 ? win.title.slice(0, 24) + "\u2026" : win.title}
      </button>
    {/each}
  </div>

  <div class="tray-area">
    <span class="clock">{time}</span>
  </div>
</div>

<style>
  .taskbar {
    display: flex;
    align-items: center;
    height: 100%;
    padding: 0 4px;
    gap: 4px;
  }

  .start-wrapper {
    position: relative;
  }

  .start-btn {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 16px;
    background: var(--accent);
    border-color: var(--accent);
    font-weight: 700;
    font-size: 13px;
    height: 36px;
  }

  .start-btn:hover {
    background: var(--accent-hover);
  }

  .start-icon {
    font-size: 16px;
  }

  .start-label {
    text-transform: lowercase;
    letter-spacing: 0.1em;
  }

  .start-menu {
    position: absolute;
    bottom: 44px;
    left: 0;
    background: var(--bg-primary);
    border: var(--border-width) solid var(--border-color);
    min-width: 200px;
    display: flex;
    flex-direction: column;
    padding: 4px 0;
  }

  .menu-header {
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.2em;
    color: var(--text-secondary);
    padding: 8px 16px 4px;
  }

  .menu-item {
    text-align: left;
    border: none;
    padding: 8px 16px;
    font-size: 13px;
    text-transform: none;
    letter-spacing: normal;
  }

  .menu-item:hover {
    background: var(--accent);
  }

  .divider {
    width: 1px;
    height: 60%;
    background: var(--border-color);
    opacity: 0.3;
  }

  .window-list {
    display: flex;
    flex: 1;
    gap: 2px;
    overflow: hidden;
    padding: 0 4px;
  }

  .window-btn {
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 11px;
    border-width: 1px;
    height: 32px;
  }

  .tray-area {
    display: flex;
    align-items: center;
    padding: 0 12px;
    border-left: 1px solid var(--border-color);
    height: 100%;
  }

  .clock {
    font-family: var(--font-mono);
    font-size: 12px;
    letter-spacing: 0.05em;
  }
</style>
