// Dev-only UI prototype served at /prototype.html. It is not a Vite build
// entry and nothing in the production viewer imports it.
import { render } from "preact";
import { simConfig } from "./catalog";
import { parseLink, Sim } from "./sim";
import { PrototypeApp } from "./viewer";
import "./prototype.css";

const sim = new Sim(simConfig, parseLink(location.search));
sim.start();
const root = document.getElementById("app");
if (root) render(<PrototypeApp sim={sim} />, root);
