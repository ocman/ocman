import { Chart as ChartJS, CategoryScale, LinearScale, LogarithmicScale, BarElement, ArcElement, Tooltip, Legend, PointElement, LineElement } from 'chart.js';

// Registered by every chart module, so chart.js loads with the analytics
// chunk instead of the app entry.
ChartJS.register(CategoryScale, LinearScale, LogarithmicScale, BarElement, ArcElement, PointElement, LineElement, Tooltip, Legend);

// Every chart fills its card and renders without animation.
ChartJS.defaults.maintainAspectRatio = false;
ChartJS.defaults.animation = false;
