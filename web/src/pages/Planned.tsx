import { Card, Kbd } from "../components/ui";

export function Planned(props: {
  title: string;
  subtitle?: string;
  milestone: number;
  lead: string;
  points: string[];
  combo?: string;
}) {
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>{props.title}</h1>
          {props.subtitle && <p className="subtitle">{props.subtitle}</p>}
        </div>
      </header>
      <Card>
        <p className="lead">{props.lead}</p>
        <ul className="points">
          {props.points.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
        {props.combo && (
          <p className="combo-line">
            Shortcut <Kbd combo={props.combo} />
          </p>
        )}
        <p className="coming">Arrives in Milestone {props.milestone}.</p>
      </Card>
    </section>
  );
}
