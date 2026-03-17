export namespace main {
	
	export class ActiveConnection {
	    machine: string;
	    service: string;
	    hubName: string;
	    pid: number;
	    output: string;
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ActiveConnection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.machine = source["machine"];
	        this.service = source["service"];
	        this.hubName = source["hubName"];
	        this.pid = source["pid"];
	        this.output = source["output"];
	        this.running = source["running"];
	    }
	}
	export class CommandLogEntry {
	    time: string;
	    description: string;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandLogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.description = source["description"];
	        this.command = source["command"];
	    }
	}
	export class ToolStatus {
	    name: string;
	    installed: boolean;
	    version: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.installed = source["installed"];
	        this.version = source["version"];
	        this.path = source["path"];
	    }
	}

}

