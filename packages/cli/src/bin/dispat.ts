#!/usr/bin/env node
'use strict'

import { launch } from '#root/lib/launch.js'

const result = launch()
if (typeof result === 'number') process.exitCode = result
