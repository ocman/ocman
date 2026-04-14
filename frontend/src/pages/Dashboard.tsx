import { useState, useEffect, useCallback, useRef } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend } from 'chart.js';
import { Bar, Doughnut } from 'react-chartjs-2';
import { api } from '../lib/api';
import type { Session, Stats, Project, ActivityDay, ModelUsage, HourlyData } from '../lib/api';
import { formatNumber, relativeTime, shortPath } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';


ChartJS.register(CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend);

export function Dashboard() {
  usePageTitle('Dashboard');
  const navigate = useNavigate();
  const tmux = useTmux();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get('tab');
  const tab = (tabParam === 'projects' || tabParam === 'stats') ? tabParam : 'sessions';
  const setTab = (t: 'sessions' | 'projects' | 'stats') => {
    setSearchParams(t === 'sessions' ? {} : { tab: t }, { replace: true });
  };
  const [stats, setStats] = useState<Stats | null>(null);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [activity, setActivity] = useState<ActivityDay[]>([]);
  const [models, setModels] = useState<ModelUsage[]>([]);
  const [hourly, setHourly] = useState<HourlyData[]>([]);
  const [loadingSessions, setLoadingSessions] = useState(true);
  const [loadingProjects, setLoadingProjects] = useState(true);
  const [loadingStats, setLoadingStats] = useState(true);
  const [loadingCharts, setLoadingCharts] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [timeRange, setTimeRange] = useState(24); // hours (0 = all)
  const [showArchived, setShowArchived] = useState(false);
  const chartsRequestedRef = useRef(false);

  const loadSessions = useCallback(async () => {
    try {
      const since = timeRange > 0 ? Date.now() - timeRange * 60 * 60 * 1000 : undefined;
      setSessions(await api.sessions(since ? { since } : {}));
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load sessions');
    }
    setLoadingSessions(false);
  }, [timeRange]);

  useEffect(() => {
    let cancelled = false;

    async function loadInitialData() {
      try {
        const [nextStats, nextProjects] = await Promise.all([
          api.stats(),
          api.projects(),
          loadSessions(),
        ]);
        if (cancelled) return;
        setStats(nextStats);
        setProjects(nextProjects);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load dashboard');
      } finally {
        if (!cancelled) {
          setLoadingStats(false);
          setLoadingProjects(false);
        }
      }
    }

    void loadInitialData();
    return () => {
      cancelled = true;
    };
  }, [loadSessions]);

  // Auto-refresh sessions every 5 seconds
  useEffect(() => {
    const id = setInterval(loadSessions, 5000);
    return () => clearInterval(id);
  }, [loadSessions]);

  // Load charts on first stats tab visit
  useEffect(() => {
    if (tab !== 'stats' || chartsRequestedRef.current) return;

    let cancelled = false;
    chartsRequestedRef.current = true;

    async function loadCharts() {
      setLoadingCharts(true);
      try {
        const [nextActivity, nextModels, nextHourly] = await Promise.all([
          api.activity(),
          api.models(),
          api.hourly(),
        ]);
        if (cancelled) return;
        setActivity(nextActivity);
        setModels(nextModels);
        setHourly(nextHourly);
      } finally {
        if (!cancelled) {
          setLoadingCharts(false);
        }
      }
    }

    void loadCharts();
    return () => {
      cancelled = true;
    };
  }, [tab]);

  const colors = ['#89b4fa', '#a6e3a1', '#cba6f7', '#fab387', '#f38ba8', '#74c7ec', '#94e2d5', '#f9e2af'];
  const sortedModels = [...models].sort((a, b) => b.count - a.count).slice(0, 8);

  return (
    <div style={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
      <div className="nav-tabs">
        <button className={`nav-tab ${tab === 'sessions' ? 'active' : ''}`} onClick={() => setTab('sessions')}>Sessions</button>
        <button className={`nav-tab ${tab === 'projects' ? 'active' : ''}`} onClick={() => setTab('projects')}>Projects</button>
        <button className={`nav-tab ${tab === 'stats' ? 'active' : ''}`} onClick={() => setTab('stats')}>Stats</button>
      </div>

      {error && (
        <div className="oc-error-banner">
          {error}
          <button onClick={() => { setError(null); loadSessions(); }}>Retry</button>
        </div>
      )}
      {tab === 'sessions' && (
        <>
          <div className="oc-time-range">
            {[{label: '12h', value: 12}, {label: '24h', value: 24}, {label: '7d', value: 168}, {label: '30d', value: 720}, {label: 'All', value: 0}].map(opt => (
              <button
                key={opt.value}
                className={`oc-time-range-btn${timeRange === opt.value ? ' active' : ''}`}
                onClick={() => { setTimeRange(opt.value); setLoadingSessions(true); }}
              >{opt.label}</button>
            ))}
            <button
              className={`oc-time-range-btn${showArchived ? ' active' : ''}`}
              onClick={() => setShowArchived(current => !current)}
            >Include archived</button>
          </div>
          <SessionTable sessions={sessions} showProject loading={loadingSessions} tmux={tmux} includeArchived={showArchived} />
        </>
      )}

      {tab === 'projects' && (
        loadingProjects ? (
          <div className="oc-list-loading">
            <div className="oc-spinner" />
            Loading projects...
          </div>
        ) : (
        <table>
          <thead>
            <tr>
              <th>Project</th>
              <th>Sessions</th>
              <th>Messages</th>
              <th>Tokens (in/out)</th>
              <th>Last Active</th>
            </tr>
          </thead>
          <tbody>
            {projects.filter(p => p.sessionCount > 0).length === 0 ? (
              <tr>
                <td colSpan={5} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
                  No projects found
                </td>
              </tr>
            ) : projects.filter(p => p.sessionCount > 0).map(p => (
              <tr key={p.directory} onClick={() => navigate(`/project/${encodeURIComponent(p.directory)}`)}>
                <td>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{shortPath(p.directory)}</span>
                    <a
                      href={`vscode://file${p.directory}`}
                      className="vscode-btn"
                      title="Open in VS Code"
                      onClick={e => e.stopPropagation()}
                    >VS Code</a>
                  </div>
                  <div className="mono">{p.directory}</div>
                </td>
                <td>{p.sessionCount}</td>
                <td>{p.messageCount}</td>
                <td className="mono">{formatNumber(p.totalTokensIn)} / {formatNumber(p.totalTokensOut)}</td>
                <td>{relativeTime(p.lastUsed)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        )
      )}

      {tab === 'stats' && (
        <>
          {loadingStats ? (
            <div className="oc-list-loading">
              <div className="oc-spinner" />
              Loading stats...
            </div>
          ) : stats && (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 16, marginBottom: 32 }}>
              <div className="stat-card"><div className="label">Sessions</div><div className="value blue">{formatNumber(stats.totalSessions)}</div></div>
              <div className="stat-card"><div className="label">Messages</div><div className="value green">{formatNumber(stats.totalMessages)}</div></div>
              <div className="stat-card"><div className="label">Projects</div><div className="value purple">{formatNumber(stats.totalProjects)}</div></div>
              <div className="stat-card"><div className="label">Tokens In</div><div className="value orange">{formatNumber(stats.totalTokensIn)}</div></div>
              <div className="stat-card"><div className="label">Tokens Out</div><div className="value blue">{formatNumber(stats.totalTokensOut)}</div></div>
              <div className="stat-card"><div className="label">Total Cost</div><div className="value green">${stats.totalCost.toFixed(2)}</div></div>
            </div>
          )}
          {loadingCharts ? (
            <div className="oc-list-loading">
              <div className="oc-spinner" />
              Loading charts...
            </div>
          ) : activity.length > 0 && <Heatmap activity={activity} />}
          {!loadingCharts && <>
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 16, marginBottom: 32 }}>
            <div className="chart-card">
              <h3>Daily Messages (last 90 days)</h3>
              <Bar data={{
                labels: activity.map(d => d.date),
                datasets: [{ label: 'Messages', data: activity.map(d => d.messages), backgroundColor: 'rgba(137, 180, 250, 0.6)', borderRadius: 2 }],
              }} options={{
                responsive: true, plugins: { legend: { display: false } },
                scales: { x: { ticks: { maxTicksLimit: 12, callback: (_, idx) => activity[idx]?.date?.slice(5) || '' }, grid: { display: false } }, y: { beginAtZero: true } },
              }} />
            </div>
            <div className="chart-card">
              <h3>Model Usage</h3>
              <Doughnut data={{
                labels: sortedModels.map(m => m.model),
                datasets: [{ data: sortedModels.map(m => m.count), backgroundColor: colors, borderWidth: 0 }],
              }} options={{
                responsive: true, plugins: { legend: { position: 'bottom', labels: { boxWidth: 12, padding: 8, font: { size: 11 } } } },
              }} />
            </div>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 16, marginBottom: 32 }}>
            <div className="chart-card">
              <h3>Sessions by Hour of Day</h3>
              <Bar data={{
                labels: hourly.map(h => h.hour + ':00'),
                datasets: [{ label: 'Sessions', data: hourly.map(h => h.sessions), backgroundColor: hourly.map(h => h.sessions > 0 ? 'rgba(166, 227, 161, 0.6)' : 'rgba(166, 227, 161, 0.1)'), borderRadius: 2 }],
              }} options={{
                responsive: true, plugins: { legend: { display: false } },
                scales: { x: { grid: { display: false } }, y: { beginAtZero: true } },
              }} />
            </div>
            <div className="chart-card">
              <h3>Tokens by Model</h3>
              <Bar data={{
                labels: sortedModels.map(m => m.model.length > 20 ? m.model.slice(0, 20) + '...' : m.model),
                datasets: [
                  { label: 'Input', data: sortedModels.map(m => m.tokensIn), backgroundColor: 'rgba(137, 180, 250, 0.6)', borderRadius: 2 },
                  { label: 'Output', data: sortedModels.map(m => m.tokensOut), backgroundColor: 'rgba(203, 166, 247, 0.6)', borderRadius: 2 },
                ],
              }} options={{
                responsive: true, indexAxis: 'y' as const,
                plugins: { legend: { position: 'bottom', labels: { boxWidth: 12, padding: 8, font: { size: 11 } } } },
                scales: { x: { beginAtZero: true, ticks: { callback: v => formatNumber(v as number) }, stacked: true }, y: { grid: { display: false }, stacked: true } },
              }} />
            </div>
          </div>
          </>}
        </>
      )}
    </div>
  );
}

function Heatmap({ activity }: { activity: ActivityDay[] }) {
  const [tooltip, setTooltip] = useState<{ text: string; x: number; y: number } | null>(null);
  const maxSessions = Math.max(...activity.map(d => d.sessions), 1);

  const weeks: (ActivityDay | null)[][] = [];
  let current: (ActivityDay | null)[] = [];

  const firstDate = new Date(activity[0]?.date || new Date());
  for (let i = 0; i < firstDate.getDay(); i++) current.push(null);

  activity.forEach(day => {
    current.push(day);
    if (current.length === 7) { weeks.push(current); current = []; }
  });
  if (current.length > 0) weeks.push(current);

  const dayNames = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];

  return (
    <div className="chart-card" style={{ marginBottom: 24 }}>
      <h3>Activity (last 90 days)</h3>
      <div style={{ display: 'flex', gap: 3 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 3, marginRight: 4 }}>
          {dayNames.map((d, i) => (
            <div key={i} style={{ width: 14, height: 14, fontSize: 10, color: 'var(--text-dim)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              {i % 2 === 1 ? d : ''}
            </div>
          ))}
        </div>
        {weeks.map((week, wi) => (
          <div key={wi} style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
            {week.map((day, di) => {
              if (!day) return <div key={di} style={{ width: 14, height: 14 }} />;
              const level = day.sessions === 0 ? 0 : Math.min(4, Math.ceil(day.sessions / maxSessions * 4));
              return (
                <div
                  key={di}
                  className="heatmap-day"
                  data-level={level}
                  onMouseEnter={e => setTooltip({ text: `${day.date}: ${day.sessions} sessions, ${day.messages} messages`, x: e.clientX + 10, y: e.clientY - 30 })}
                  onMouseLeave={() => setTooltip(null)}
                />
              );
            })}
            {Array.from({ length: 7 - week.length }).map((_, i) => (
              <div key={`pad-${i}`} style={{ width: 14, height: 14 }} />
            ))}
          </div>
        ))}
      </div>
      {tooltip && (
        <div style={{ position: 'fixed', left: tooltip.x, top: tooltip.y, background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 6, padding: '6px 10px', fontSize: 12, pointerEvents: 'none', zIndex: 100 }}>
          {tooltip.text}
        </div>
      )}
    </div>
  );
}
