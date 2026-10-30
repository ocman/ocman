import { useSessionFilterPreference } from './useSessionFilterPreference';
import { SESSION_FILTER_DEFAULTS } from './sessionListFilters';
import { useBetaFeatures } from './betaFeatures';

/** Archive state belongs to each list controller; the remaining preferences are shared. */
export function useSessionListFilters() {
  const factory = useBetaFeatures((s) => s.factory);
  const [alwaysShowPrompts, setAlwaysShowPrompts] = useSessionFilterPreference('prompts', SESSION_FILTER_DEFAULTS.alwaysShowPrompts);
  const [showChildren, setShowChildren] = useSessionFilterPreference('children', SESSION_FILTER_DEFAULTS.showChildren);
  const [showFactory, setShowFactory] = useSessionFilterPreference('factory', SESSION_FILTER_DEFAULTS.showFactory);
  const [showRoutines, setShowRoutines] = useSessionFilterPreference('routines', SESSION_FILTER_DEFAULTS.showRoutines);
  const [showPlugins, setShowPlugins] = useSessionFilterPreference('plugins', SESSION_FILTER_DEFAULTS.showPlugins);
  const [showSnoozed, setShowSnoozed] = useSessionFilterPreference('snoozed', SESSION_FILTER_DEFAULTS.showSnoozed);
  return { alwaysShowPrompts, setAlwaysShowPrompts, showChildren, setShowChildren, showFactory: factory && showFactory, setShowFactory,
    showRoutines, setShowRoutines, showPlugins, setShowPlugins, showSnoozed, setShowSnoozed };
}
