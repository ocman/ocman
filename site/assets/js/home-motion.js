(() => {
  const home = document.querySelector('.oc-home');
  if (!home) return;
  const hero = home.querySelector('.oc-hero');
  const scenes = [...home.querySelectorAll('.oc-scene')];
  let frame = null;
  const clamp = value => Math.max(0, Math.min(1, value));

  const render = () => {
    frame = null;
    // Read layout before writing transforms; one update per animation frame.
    const rect = hero.getBoundingClientRect();
    const progress = clamp(-rect.top / rect.height);
    const compact = innerWidth <= 640;
    const positions = scenes.filter(scene => !scene.hidden).map(scene => ({
      visual: scene.querySelector('.oc-scene-visual'),
      progress: clamp((innerHeight - scene.getBoundingClientRect().top) / innerHeight),
    }));
    home.style.setProperty('--oc-hero-tilt', `${(compact ? 3 : 9) * (1 - progress)}deg`);
    home.style.setProperty('--oc-hero-shift', `${progress * (compact ? -45 : -110)}px`);
    home.style.setProperty('--oc-hero-scale', `${1 - progress * 0.04}`);
    home.style.setProperty('--oc-aurora-drift', `${progress * 260}px`);
    for (const position of positions) {
      position.visual.style.setProperty('--oc-scene-shift', `${(1 - position.progress) * (compact ? 25 : 65)}px`);
    }
  };

  const schedule = () => {
    if (frame === null) frame = requestAnimationFrame(render);
  };

  addEventListener('scroll', schedule, { passive: true });
  addEventListener('resize', schedule, { passive: true });
  home.addEventListener('oc-tour-change', schedule);
  home.dataset.motion = 'on';
  schedule();
})();
