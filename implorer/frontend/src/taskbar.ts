import Taskbar from "./lib/Taskbar.svelte";
import { mount } from "svelte";

mount(Taskbar, { target: document.getElementById("app")! });
