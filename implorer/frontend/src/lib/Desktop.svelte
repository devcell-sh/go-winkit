<script lang="ts">
  let greeting = $state("implorer");
</script>

<div class="desktop">
  <div class="watermark">
    <span class="logo">{greeting}</span>
    <span class="sub">winkit desktop shell</span>
  </div>
</div>

<style>
  .desktop {
    width: 100vw;
    height: 100vh;
    background: var(--bg-primary);
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .watermark {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    opacity: 0.15;
  }

  .logo {
    font-family: var(--font-mono);
    font-size: 72px;
    font-weight: 700;
    text-transform: lowercase;
    letter-spacing: 0.15em;
    color: var(--text-primary);
  }

  .sub {
    font-family: var(--font-mono);
    font-size: 14px;
    text-transform: uppercase;
    letter-spacing: 0.3em;
    color: var(--text-secondary);
  }
</style>
