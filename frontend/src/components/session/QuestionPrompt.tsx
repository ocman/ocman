import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { markHandledByPrompt, wasHandledByPrompt, type PromptKeyEvent as KeyEvent } from './promptKeyboard';
import './QuestionPrompt.css';

function optionIndexFromNumberKey(e: KeyEvent, max: number): number {
  const codeMatch = /^Digit([1-9])$/.exec(e.code) ?? /^Numpad([1-9])$/.exec(e.code);
  if (codeMatch) {
    const idx = Number(codeMatch[1]) - 1;
    return idx < max ? idx : -1;
  }
  if (e.key >= '1' && e.key <= '9') {
    const idx = Number(e.key) - 1;
    return idx < max ? idx : -1;
  }
  return -1;
}

interface QuestionOption {
  label: string;
  description: string;
}

export interface QuestionItem {
  question: string;
  header: string;
  options: QuestionOption[];
  multiple?: boolean;
  custom?: boolean;
}

export interface PendingQuestion {
  requestId: string;
  sessionID: string;
  questions: QuestionItem[];
}

// Row index for the custom-answer input, always positioned after all options.
const CUSTOM_ROW = -1;

export function QuestionPrompt({
  question,
  onReply,
  onReject,
  disabled,
  error,
}: {
  question: PendingQuestion;
  onReply: (answers: string[][]) => void;
  onReject: () => void;
  disabled?: boolean;
  error?: string | null;
}) {
  const [selectedIndices, setSelectedIndices] = useState<Record<number, number | null>>({});
  const [customTexts, setCustomTexts] = useState<Record<number, string>>({});
  const [currentStep, setCurrentStep] = useState(0);
  // Keyboard-focused row (option index or CUSTOM_ROW). Distinct from the
  // committed "selected" answer — focus moves freely with arrow keys,
  // selection only updates on Enter/Space/number-key.
  const [focusedRow, setFocusedRow] = useState<number>(0);

  const wrapRef = useRef<HTMLDivElement>(null);
  const customInputRef = useRef<HTMLInputElement>(null);
  const handleKeyDownRef = useRef<((e: KeyEvent) => void) | null>(null);

  const totalSteps = question.questions.length;
  const isStepped = totalSteps > 1;
  const currentQ = question.questions[currentStep];

  const isStepAnswered = useCallback((qi: number) => {
    const sel = selectedIndices[qi];
    const custom = customTexts[qi]?.trim();
    const q = question.questions[qi];
    return (sel != null && sel >= 0 && (!q || sel < q.options.length)) || !!custom;
  }, [selectedIndices, customTexts, question.questions]);

  // Compute the full answers array for a given override of the selectedIndices
  // map. Used by the click handler to auto-advance/submit with the freshly
  // chosen option without waiting for React state to flush.
  const computeAnswers = useCallback((indicesOverride?: Record<number, number | null>): string[][] => {
    const indices = indicesOverride ?? selectedIndices;
    return question.questions.map((q, qi) => {
      const sel = indices[qi];
      const custom = customTexts[qi]?.trim();
      if (sel != null && sel >= 0 && sel < q.options.length) {
        return [q.options[sel].label];
      }
      if (custom) return [custom];
      return [];
    });
  }, [question.questions, selectedIndices, customTexts]);

  const getAnswers = useCallback(() => computeAnswers(), [computeAnswers]);

  const goNext = useCallback(() => {
    if (currentStep < totalSteps - 1) setCurrentStep(s => s + 1);
  }, [currentStep, totalSteps]);

  const goPrev = useCallback(() => {
    if (currentStep > 0) setCurrentStep(s => s - 1);
  }, [currentStep]);

  const submit = useCallback(() => {
    if (disabled) return;
    const answers = getAnswers();
    if (answers.every(a => a.length > 0)) onReply(answers);
  }, [disabled, getAnswers, onReply]);

  // Select an option, then auto-advance (or submit on the last step). Used by
  // click and keyboard selection — lets the user answer an entire multi-step
  // question flow in N clicks without pressing Enter each time.
  const selectOption = useCallback((qi: number, oi: number) => {
    if (disabled) return;
    const nextIndices = { ...selectedIndices, [qi]: oi };
    setSelectedIndices(nextIndices);
    setCustomTexts(prev => ({ ...prev, [qi]: '' }));

    // Auto-advance only when selecting on the current step (guards against
    // stale clicks during animations).
    if (qi !== currentStep) return;

    if (qi < totalSteps - 1) {
      setCurrentStep(qi + 1);
      return;
    }
    // Last step — submit if every question has an answer.
    const answers = computeAnswers(nextIndices);
    if (answers.every(a => a.length > 0)) onReply(answers);
  }, [disabled, selectedIndices, currentStep, totalSteps, computeAnswers, onReply]);

  // Reset row focus & move DOM focus onto the wrapper whenever the visible
  // step changes (or on mount). Sync focusedRow to whatever's currently
  // selected so keyboard nav continues from the user's last choice.
  useLayoutEffect(() => {
    const sel = selectedIndices[currentStep];
    if (sel != null && sel >= 0) setFocusedRow(sel);
    else if (customTexts[currentStep]?.trim()) setFocusedRow(CUSTOM_ROW);
    else setFocusedRow(0);
    wrapRef.current?.focus();
    // Intentionally not depending on selectedIndices/customTexts — we only
    // want to re-sync when the user navigates to a new step.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentStep]);

  const moveFocus = useCallback((direction: 1 | -1) => {
    if (!currentQ) return;
    const rows: number[] = [...currentQ.options.map((_, i) => i), CUSTOM_ROW];
    const currentIdx = rows.indexOf(focusedRow);
    const nextIdx = (currentIdx + direction + rows.length) % rows.length;
    const next = rows[nextIdx];
    setFocusedRow(next);
    if (next === CUSTOM_ROW) customInputRef.current?.focus();
    else {
      // Blur any focused input so the container handles keys again.
      if (document.activeElement instanceof HTMLElement &&
          document.activeElement !== wrapRef.current) {
        document.activeElement.blur();
      }
      wrapRef.current?.focus();
    }
  }, [currentQ, focusedRow]);

  const handleKeyDown = useCallback((e: KeyEvent) => {
    if (disabled || wasHandledByPrompt(e)) return;
    const target = e.target instanceof HTMLElement ? e.target : null;
    const isInputFocused = target?.tagName === 'INPUT';

    // Escape always dismisses, even from inside the text input.
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      onReject();
      return;
    }

    // Enter: commit the currently focused row and advance/submit.
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (isInputFocused) {
        // Typed custom answer — clear any previously selected option, then
        // advance/submit if the text is non-empty.
        if (customTexts[currentStep]?.trim()) {
          setSelectedIndices(prev => ({ ...prev, [currentStep]: null }));
          if (currentStep < totalSteps - 1) goNext();
          else submit();
        }
        return;
      }
      if (focusedRow >= 0 && focusedRow < currentQ.options.length) {
        // selectOption auto-advances or submits as appropriate.
        selectOption(currentStep, focusedRow);
        return;
      }
      // Nothing focused to commit — advance/submit based on existing state.
      if (isStepped && currentStep < totalSteps - 1) {
        if (isStepAnswered(currentStep)) goNext();
      } else {
        submit();
      }
      return;
    }

    if (isInputFocused) return; // let the input handle everything else

    if (e.key === ' ') {
      if (focusedRow >= 0 && focusedRow < currentQ.options.length) {
        e.preventDefault();
        selectOption(currentStep, focusedRow);
      }
      return;
    }

    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
      e.preventDefault();
      moveFocus(1);
      return;
    }
    if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
      e.preventDefault();
      moveFocus(-1);
      return;
    }

    // Number keys 1-9 directly select the matching option.
    const idx = optionIndexFromNumberKey(e, currentQ.options.length);
    if (idx >= 0 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      selectOption(currentStep, idx);
      setFocusedRow(idx);
      return;
    }

    // Tab between steps
    if (isStepped) {
      if ((e.key === 'n' || e.key === 'N') && isStepAnswered(currentStep)) {
        e.preventDefault();
        goNext();
      } else if (e.key === 'p' || e.key === 'P') {
        e.preventDefault();
        goPrev();
      }
    }
  }, [currentQ.options.length, currentStep, disabled, goNext, goPrev, isStepAnswered, isStepped, moveFocus, onReject, selectOption, submit, totalSteps, focusedRow, customTexts]);

  // Keep a stable ref so the window listener always calls the latest version
  // of handleKeyDown, avoiding the "stale closure" problem when deps change
  // due to SSE updates racing with a keypress.
  useLayoutEffect(() => {
    handleKeyDownRef.current = handleKeyDown;
  });

  useLayoutEffect(() => {
    const onWindowKeyDown = (e: KeyboardEvent) => {
      if (wasHandledByPrompt(e)) return;
      handleKeyDownRef.current?.(e);
      markHandledByPrompt(e);
    };
    window.addEventListener('keydown', onWindowKeyDown, true);
    return () => window.removeEventListener('keydown', onWindowKeyDown, true);
  }, []);

  const allAnswered = getAnswers().every(a => a.length > 0);
  const isLastStep = currentStep === totalSteps - 1;

  return (
    <div
      ref={wrapRef}
      className="oc-question-wrap"
      tabIndex={-1}
      role="dialog"
      aria-label="Pending question"
      onKeyDown={handleKeyDown}
    >
      <div className="oc-question-box">
        {isStepped && (
          <div className="oc-question-step-indicator">
            {question.questions.map((_, si) => (
              <button
                key={si}
                type="button"
                className={`oc-question-step-dot${si === currentStep ? ' oc-question-step-active' : ''}${isStepAnswered(si) ? ' oc-question-step-done' : ''}`}
                onClick={() => setCurrentStep(si)}
                disabled={disabled}
                title={`Question ${si + 1}`}
                tabIndex={-1}
              />
            ))}
            <span className="oc-question-step-label">{currentStep + 1} / {totalSteps}</span>
          </div>
        )}
        <div className="oc-question-box-text">{currentQ.question}</div>
        <div className="oc-question-box-options" role="radiogroup">
          {currentQ.options.map((opt, oi) => {
            const selected = selectedIndices[currentStep] === oi;
            const focused = focusedRow === oi;
            return (
              <button
                key={oi}
                type="button"
                role="radio"
                aria-checked={selected}
                className={`oc-question-opt-btn${selected ? ' oc-question-opt-selected' : ''}${focused ? ' oc-question-opt-focused' : ''}`}
                onClick={() => { selectOption(currentStep, oi); setFocusedRow(oi); }}
                onMouseEnter={() => setFocusedRow(oi)}
                disabled={disabled}
                tabIndex={-1}
              >
                <span className="oc-question-opt-num">{oi + 1}.</span>
                <span className="oc-question-opt-content">
                  <span className="oc-question-opt-label">{opt.label}</span>
                  {opt.description && (
                    <span className="oc-question-opt-desc">{opt.description}</span>
                  )}
                </span>
              </button>
            );
          })}
          <div
            className={`oc-question-opt-custom${selectedIndices[currentStep] === null && customTexts[currentStep]?.trim() ? ' oc-question-opt-custom-active' : ''}${focusedRow === CUSTOM_ROW ? ' oc-question-opt-focused' : ''}`}
          >
            <span className="oc-question-opt-num">{currentQ.options.length + 1}.</span>
            <input
              ref={customInputRef}
              type="text"
              className="oc-question-inline-input"
              placeholder="Type your own answer"
              value={customTexts[currentStep] || ''}
              onChange={(e) => setCustomTexts(prev => ({ ...prev, [currentStep]: e.target.value }))}
              onFocus={() => {
                setFocusedRow(CUSTOM_ROW);
                setSelectedIndices(prev => ({ ...prev, [currentStep]: null }));
              }}
              disabled={disabled}
            />
          </div>
        </div>
        <div className="oc-question-box-actions">
          {isStepped && currentStep > 0 && (
            <button
              type="button"
              className="oc-question-dismiss-btn"
              onClick={goPrev}
              disabled={disabled}
              tabIndex={-1}
            >Back</button>
          )}
          {isStepped && !isLastStep ? (
            <button
              type="button"
              className="oc-question-submit-btn"
              onClick={goNext}
              disabled={disabled || !isStepAnswered(currentStep)}
              tabIndex={-1}
            >Next</button>
          ) : (
            <button
              type="button"
              className="oc-question-submit-btn"
              onClick={submit}
              disabled={disabled || !allAnswered}
              tabIndex={-1}
            >Submit</button>
          )}
          <button
            type="button"
            className="oc-question-dismiss-btn"
            onClick={onReject}
            disabled={disabled}
            tabIndex={-1}
          >Dismiss</button>
          <span className="oc-question-keys">
            <kbd>↑↓</kbd> move &middot; <kbd>1-9</kbd> pick &middot; <kbd>enter</kbd> {isStepped && !isLastStep ? 'next' : 'submit'} &middot; <kbd>esc</kbd> dismiss
          </span>
        </div>
        {error && (
          <div className="oc-question-error">{error}</div>
        )}
      </div>
    </div>
  );
}
