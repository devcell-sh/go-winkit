import Desktop from "./lib/Desktop.svelte";
import { mount } from "svelte";

mount(Desktop, { target: document.getElementById("app")! });
