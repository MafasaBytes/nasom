// Verdict copy for Surface B (DESIGN §4.2). The three verdicts map to the three statuses, so the
// checker and monitor speak one visual language. Exhaustive over DefensibilityStatus.

import type { DefensibilityStatus } from "../../types/api";

export interface VerdictCopy {
  heading: string;
  sub: string;
}

export function verdictCopy(status: DefensibilityStatus): VerdictCopy {
  switch (status) {
    case "defensible":
      return {
        heading: "Vergunningsvrij haalbaar",
        sub: "Depositie onder de rekenkundige ondergrens. Voortoets volstaat naar verwachting.",
      };
    case "attention":
      return {
        heading: "Haalbaar mét mitigatie",
        sub: "Beperkte depositie. Met de juiste maatregelen waarschijnlijk vergunbaar — gevoelig voor versie- en beleidswijzigingen.",
      };
    case "exposed":
      return {
        heading: "Vergunningplichtig — passende beoordeling",
        sub: "Substantiële depositie op overbelast gebied. Natuurvergunning en mitigatie/saldering vereist; reken op een langer traject.",
      };
    default: {
      const exhaustive: never = status;
      return exhaustive;
    }
  }
}
