(() => {
  const home = document.querySelector('.oc-home');
  if (!home) return;
  const list = home.querySelector('.oc-chapters');
  const tabs = [...list.querySelectorAll('a')];
  const panels = tabs.map(tab => document.getElementById(tab.hash.slice(1)));
  const select = (index, { focus = false, updateURL = false } = {}) => {
    tabs.forEach((tab, position) => {
      const active = position === index;
      tab.setAttribute('aria-selected', String(active));
      tab.tabIndex = active ? 0 : -1;
      panels[position].hidden = !active;
    });
    if (focus) tabs[index].focus({ preventScroll: true });
    if (updateURL) {
      const url = new URL(location.href);
      url.hash = panels[index].id;
      history.replaceState(null, '', url);
    }
    home.dispatchEvent(new Event('oc-tour-change'));
  };

  list.setAttribute('role', 'tablist');
  tabs.forEach((tab, index) => {
    tab.id = `tour-tab-${panels[index].id}`;
    tab.setAttribute('role', 'tab');
    tab.setAttribute('aria-controls', panels[index].id);
    panels[index].setAttribute('role', 'tabpanel');
    panels[index].setAttribute('aria-labelledby', tab.id);
    panels[index].tabIndex = 0;
    tab.addEventListener('click', event => {
      // Preserve native open-in-new-tab behavior for modified link clicks.
      if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
      event.preventDefault();
      select(index, { updateURL: true });
    });
    tab.addEventListener('keydown', event => {
      const destinations = {
        ArrowRight: (index + 1) % tabs.length,
        ArrowLeft: (index + tabs.length - 1) % tabs.length,
        Home: 0,
        End: tabs.length - 1,
      };
      if (event.key in destinations) {
        event.preventDefault();
        select(destinations[event.key], { focus: true, updateURL: true });
      } else if (event.key === ' ') {
        event.preventDefault();
        select(index, { updateURL: true });
      }
    });
  });
  const selectHash = () => {
    const index = tabs.findIndex(tab => tab.hash === location.hash);
    if (index >= 0) select(index);
    return index;
  };
  home.dataset.tour = 'tabs';
  if (selectHash() < 0) select(0);
  addEventListener('hashchange', selectHash);
})();
