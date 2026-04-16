import { useState } from 'react';
import './QuestionPrompt.css';

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

  const totalSteps = question.questions.length;
  const isStepped = totalSteps > 1;

  const isStepAnswered = (qi: number) => {
    const sel = selectedIndices[qi];
    const custom = customTexts[qi]?.trim();
    const q = question.questions[qi];
    return (sel != null && sel >= 0 && (!q || sel < q.options.length)) || !!custom;
  };

  const handleOptionClick = (qi: number, oi: number) => {
    if (disabled) return;
    setSelectedIndices(prev => ({ ...prev, [qi]: oi }));
    setCustomTexts(prev => ({ ...prev, [qi]: '' }));
  };

  const handleCustomFocus = (qi: number) => {
    setSelectedIndices(prev => ({ ...prev, [qi]: null }));
  };

  const getAnswers = (): string[][] =>
    question.questions.map((q, qi) => {
      const sel = selectedIndices[qi];
      const custom = customTexts[qi]?.trim();
      if (sel != null && sel >= 0 && sel < q.options.length) {
        return [q.options[sel].label];
      }
      if (custom) return [custom];
      return [];
    });

  const handleSubmit = () => {
    if (disabled) return;
    const answers = getAnswers();
    if (answers.every(a => a.length > 0)) {
      onReply(answers);
    }
  };

  const handleNext = () => {
    if (currentStep < totalSteps - 1) {
      setCurrentStep(currentStep + 1);
    }
  };

  const handlePrev = () => {
    if (currentStep > 0) {
      setCurrentStep(currentStep - 1);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onReject();
    } else if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (isStepped && currentStep < totalSteps - 1) {
        if (isStepAnswered(currentStep)) handleNext();
      } else {
        handleSubmit();
      }
    }
  };

  const allAnswered = getAnswers().every(a => a.length > 0);
  const isLastStep = currentStep === totalSteps - 1;

  const renderStep = (qi: number) => {
    const q = question.questions[qi];
    return (
      <div key={qi} className="oc-question-box">
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
              />
            ))}
            <span className="oc-question-step-label">{currentStep + 1} / {totalSteps}</span>
          </div>
        )}
        <div className="oc-question-box-text">{q.question}</div>
        <div className="oc-question-box-options">
          {q.options.map((opt, oi) => (
            <button
              key={oi}
              type="button"
              className={`oc-question-opt-btn${selectedIndices[qi] === oi ? ' oc-question-opt-selected' : ''}`}
              onClick={() => handleOptionClick(qi, oi)}
              disabled={disabled}
            >
              <span className="oc-question-opt-num">{oi + 1}.</span>
              <span className="oc-question-opt-content">
                <span className="oc-question-opt-label">{opt.label}</span>
                {opt.description && (
                  <span className="oc-question-opt-desc">{opt.description}</span>
                )}
              </span>
            </button>
          ))}
          <div className={`oc-question-opt-custom${selectedIndices[qi] === null && customTexts[qi]?.trim() ? ' oc-question-opt-custom-active' : ''}`}>
            <span className="oc-question-opt-num">{q.options.length + 1}.</span>
            <input
              type="text"
              className="oc-question-inline-input"
              placeholder="Type your own answer"
              value={customTexts[qi] || ''}
              onChange={(e) => setCustomTexts(prev => ({ ...prev, [qi]: e.target.value }))}
              onFocus={() => handleCustomFocus(qi)}
              disabled={disabled}
            />
          </div>
        </div>
        <div className="oc-question-box-actions">
          {isStepped && currentStep > 0 && (
            <button
              type="button"
              className="oc-question-dismiss-btn"
              onClick={handlePrev}
              disabled={disabled}
            >Back</button>
          )}
          {isStepped && !isLastStep ? (
            <button
              type="button"
              className="oc-question-submit-btn"
              onClick={handleNext}
              disabled={disabled || !isStepAnswered(currentStep)}
            >Next</button>
          ) : (
            <button
              type="button"
              className="oc-question-submit-btn"
              onClick={handleSubmit}
              disabled={disabled || !allAnswered}
            >Submit</button>
          )}
          <button
            type="button"
            className="oc-question-dismiss-btn"
            onClick={onReject}
            disabled={disabled}
          >Dismiss</button>
          <span className="oc-question-keys">
            <kbd>enter</kbd> {isStepped && !isLastStep ? 'next' : 'submit'} &middot; <kbd>esc</kbd> dismiss
          </span>
        </div>
        {error && (
          <div className="oc-question-error">{error}</div>
        )}
      </div>
    );
  };

  return (
    <div className="oc-question-wrap" onKeyDown={handleKeyDown}>
      {renderStep(currentStep)}
    </div>
  );
}
