// Served when the UI has not been built. Build it with
// `make install-plugin PLUGIN=catalog` or `pnpm --dir ui build`.
import { jsx } from '/plugin-shared/react-jsx-runtime.js';
import { EmptyState, Page } from '/plugin-shared/ocman-plugin-ui.js';

export default function Index() {
  return jsx(Page, {
    title: 'Catalog',
    children: jsx(EmptyState, { children: 'This build has no compiled interface. Run make install-plugin PLUGIN=catalog.' }),
  });
}
