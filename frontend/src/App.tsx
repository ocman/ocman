import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom';
import { Dashboard } from './pages/Dashboard';
import { ProjectDetail } from './pages/ProjectDetail';
import { SessionDetail } from './pages/SessionDetail';
import { HeaderProvider, useHeaderInfo } from './lib/headerContext';

function Header() {
  const location = useLocation();
  const path = location.pathname;
  const { info } = useHeaderInfo();

  let breadcrumb: React.ReactNode = '';
  if (path.startsWith('/session/')) {
    breadcrumb = (
      <>
        / <Link to="/" style={{ color: 'inherit' }}>Sessions</Link>
        {info.sessionTitle && <> / {info.sessionTitle}</>}
      </>
    );
  } else if (path.startsWith('/project/')) {
    const dir = decodeURIComponent(path.slice('/project/'.length));
    const name = dir.split('/').pop();
    breadcrumb = (
      <>/ <Link to="/" style={{ color: 'inherit' }}>Sessions</Link> / {name}</>
    );
  }

  return (
    <header>
      <h1>
        <Link to="/" style={{ color: 'inherit', textDecoration: 'none' }}>ocman</Link>{' '}
        <span>{breadcrumb}</span>
      </h1>
      {path.startsWith('/session/') && info.stats && info.stats.length > 0 && (
        <div className="header-stats">
          {info.stats.map((s, i) => (
            <span key={i} className="header-stat">
              <span className="header-stat-label">{s.label}</span>
              <span className="header-stat-value">{s.value}</span>
            </span>
          ))}
        </div>
      )}
    </header>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <HeaderProvider>
        <div className="container">
          <Header />
          <div className="content">
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/project/*" element={<ProjectDetail />} />
              <Route path="/session/:id" element={<SessionDetail />} />
            </Routes>
          </div>
        </div>
      </HeaderProvider>
    </BrowserRouter>
  );
}
