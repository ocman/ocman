import type { QuestionItem, QuestionOption } from '../../lib/questions';
import styles from './AnsweredQuestionBlock.module.css';

// Prefer an exact label, since labels can themselves contain commas. Only
// split a multi-answer when every segment matches an offered choice.
function selectedChoices(question: QuestionItem, answer: string): QuestionOption[] {
  if (!answer) return [];
  const exact = question.options.find((option) => option.label === answer);
  if (exact) return [exact];
  const options = [...question.options].sort((a, b) => b.label.length - a.label.length);
  const selected: QuestionOption[] = [];
  let remaining = answer;
  while (remaining) {
    const option = options.find(({ label }) => label && (remaining === label || remaining.startsWith(`${label}, `)));
    if (!option) return [{ label: answer, description: '' }];
    selected.push(option);
    remaining = remaining.slice(option.label.length + 2);
  }
  return selected.map(({ label }) => label).join(', ') === answer
    ? selected
    : [{ label: answer, description: '' }];
}

export function AnsweredQuestionBlock({ questions, answers }: { questions: QuestionItem[]; answers: string[] }) {
  return (
    <section className={styles.list} aria-label="Answered questions">
      <div className={styles.heading}>
        <i className="bi bi-question-circle" aria-hidden="true" />
        <span>Questions answered</span>
        <span className={styles.count}>{questions.length}</span>
      </div>
      {questions.map((question, index) => {
        const answer = answers[index] || '';
        const choices = selectedChoices(question, answer);
        return (
          <article key={index} className={styles.card} aria-label={question.question}>
            {question.header && <div className={styles.eyebrow}>{question.header}</div>}
            <div className={styles.question}>{question.question}</div>
            {choices.map((choice, choiceIndex) => (
              <div key={choiceIndex} className={styles.answer}>
                <i className={`bi bi-check-circle ${styles.check}`} aria-hidden="true" />
                <div className={styles.choice}>
                  <div className={styles.label}>{choice.label}</div>
                  {choice.description && <div className={styles.description}>{choice.description}</div>}
                </div>
              </div>
            ))}
            {!answer && <div className={styles.description}>No answer recorded</div>}
          </article>
        );
      })}
    </section>
  );
}
